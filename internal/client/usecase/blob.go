package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type BlobSource string

const (
	BlobWorking  BlobSource = "working"
	BlobSnapshot BlobSource = "snapshot"
	BlobRevision BlobSource = "revision"
)

// BlobRequest names one file version: the working copy, the last synced
// snapshot (the old side of a working diff, offline) or a revision.
type BlobRequest struct {
	Source   BlobSource
	Revision string
	Path     string
}

var ErrFileMissing = errors.New("file does not exist")

// ReadBlob resolves one file version and hands its entry and decoded content
// to fn. The content is streamed chunk by chunk; missing revision chunks are
// downloaded into the local object store first.
func (d *Diff) ReadBlob(ctx context.Context, root string, req BlobRequest, fn func(diff.Entry, io.Reader) error) error {
	p, err := cleanBlobPath(req.Path)
	if err != nil {
		return err
	}
	if err := d.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(d.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	switch req.Source {
	case "", BlobWorking:
		return readWorkingBlob(root, p, fn)
	case BlobSnapshot:
		snapshot, err := d.localRepo.Snapshot()
		if err != nil {
			return err
		}
		entry, ok := snapshotEntries(snapshot, DiffOptions{Paths: []string{p}}, nil)[p]
		if !ok {
			return missingFile(fmt.Sprintf("%s is not tracked", p))
		}
		return fn(entry, &blobReader{load: d.localRepo.LoadChunk, entry: entry})
	case BlobRevision:
		return d.readRevisionBlob(ctx, req.Revision, p, fn)
	default:
		return clientDomain.NewUserError(fmt.Sprintf("unknown blob source %q", req.Source))
	}
}

func (d *Diff) readRevisionBlob(ctx context.Context, token, p string, fn func(diff.Entry, io.Reader) error) error {
	cfg, err := d.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if err := d.client.Connect(ctx, nu.Origin); err != nil {
		return err
	}
	if err := d.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}
	rev, err := d.resolveRevision(ctx, nu, cfg, token)
	if err != nil {
		return err
	}
	entry, ok := rev.entries[p]
	if !ok {
		return missingFile(fmt.Sprintf("%s does not exist in %s", p, token))
	}
	missing, err := d.localRepo.MissingChunks(entry.ChunkHashes)
	if err != nil {
		return err
	}
	scope := clientDomain.ChunkScope{Org: nu.Org, Project: nu.Project, CommitIDs: commitIDs(rev.commitID), Paths: []string{p}}
	if err := downloadMissing(ctx, d.client, d.localRepo, scope, missing, entry.SizeBytes); err != nil {
		return err
	}
	return fn(entry, &blobReader{load: d.localRepo.LoadChunk, entry: entry})
}

func missingFile(msg string) error {
	e := clientDomain.NewNotFoundError(msg)
	e.Cause = ErrFileMissing
	return e
}

func readWorkingBlob(root, p string, fn func(diff.Entry, io.Reader) error) error {
	full, err := resolveInsideRoot(root, p)
	if err != nil {
		return err
	}
	pre, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return missingFile(fmt.Sprintf("%s does not exist", p))
		}
		return err
	}
	if !pre.Mode().IsRegular() {
		return clientDomain.NewUserError(fmt.Sprintf("%s is not a file", p))
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return missingFile(fmt.Sprintf("%s does not exist", p))
		}
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return clientDomain.NewUserError(fmt.Sprintf("%s is not a file", p))
	}
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return err
	}
	entry := diff.Entry{Path: p, Mode: serverModeFromPerm(info.Mode()), SizeBytes: info.Size(), IsBinary: isBinary}
	return fn(entry, f)
}

// resolveInsideRoot joins p onto root and refuses results that leave the
// clone through a symlink.
func resolveInsideRoot(root, p string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", missingFile(fmt.Sprintf("%s does not exist", p))
		}
		return "", err
	}
	rel, err := filepath.Rel(realRoot, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", clientDomain.NewUserError(fmt.Sprintf("%s resolves outside the repository", p))
	}
	if isMetadataPath(filepath.ToSlash(rel)) {
		return "", clientDomain.NewUserError("repository metadata cannot be read")
	}
	return full, nil
}

func cleanBlobPath(p string) (string, error) {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" || path.IsAbs(p) || filepath.IsAbs(p) {
		return "", clientDomain.NewUserError("a repository-relative path is required")
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", clientDomain.NewUserError("a repository-relative path is required")
	}
	if isMetadataPath(p) {
		return "", clientDomain.NewUserError("repository metadata cannot be read")
	}
	return p, nil
}

func isMetadataPath(p string) bool {
	p = strings.ToLower(p)
	return p == ".nipa" || strings.HasPrefix(p, ".nipa/")
}

// blobReader streams a stored file by loading and decoding one chunk at a
// time, so large assets never sit in memory whole.
type blobReader struct {
	load  func(serverDomain.Hash) ([]byte, error)
	entry diff.Entry
	next  int
	buf   []byte
}

func (r *blobReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.next >= len(r.entry.ChunkHashes) {
			return 0, io.EOF
		}
		h := r.entry.ChunkHashes[r.next]
		r.next++
		data, err := r.load(h)
		if err != nil {
			return 0, fmt.Errorf("load chunk %s for %s: %w", h, r.entry.Path, err)
		}
		if r.buf, err = chunker.Decode(r.entry.Encoding, data); err != nil {
			return 0, fmt.Errorf("decode chunk %s for %s: %w", h, r.entry.Path, err)
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
