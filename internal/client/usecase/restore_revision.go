package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type RevisionRestoreRepo interface {
	diffLocalRepo
	LoadMergeState() (*clientDomain.MergeState, error)
	LoadRevertState() (*clientDomain.RevertState, error)
	SnapshotFile(p string) (clientDomain.SnapshotFile, bool, error)
}

// RevisionRestore writes one file as it was in a commit over the working
// file ("get this version"). Nothing is staged: the file shows as modified.
type RevisionRestore struct {
	diff *Diff
	repo RevisionRestoreRepo
}

func NewRevisionRestore(auth *Auth, client diffClient, repo RevisionRestoreRepo) *RevisionRestore {
	return &RevisionRestore{diff: NewDiff(auth, client, repo), repo: repo}
}

func (r *RevisionRestore) Run(ctx context.Context, root, path, commitID string, progress ...DownloadProgress) (err error) {
	p, err := cleanBlobPath(path)
	if err != nil {
		return err
	}
	commitID = strings.TrimSpace(commitID)
	if _, perr := snow.ParseBase36(commitID); commitID == "" || perr != nil {
		return clientDomain.NewUserError("a commit id is required")
	}
	if err := r.repo.Init(root); err != nil {
		return err
	}
	defer func() { pruneAfterSuccess(r.repo, err) }()
	unlock, err := lockShared(r.repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.refusePending(); err != nil {
		return err
	}
	cfg, err := r.repo.LoadConfig()
	if err != nil {
		return err
	}
	if !sparseCovers(cfg.Sparse, p) {
		return clientDomain.NewUserError(fmt.Sprintf("%s is outside this clone's checkout; add its folder to the checkout first", p))
	}
	if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil && info.IsDir() {
		return clientDomain.NewUserError(fmt.Sprintf("%s is a folder", p))
	}

	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if err := r.diff.client.Connect(ctx, nu.Origin); err != nil {
		return err
	}
	if err := r.diff.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}
	rev, err := r.diff.revisionByCommit(ctx, nu, commitID)
	if err != nil {
		if isNotFoundError(err) {
			return clientDomain.NewUserError(fmt.Sprintf("revision %q not found", commitID))
		}
		return err
	}
	entry, ok := rev.entries[p]
	if !ok {
		return missingFile(fmt.Sprintf("%s does not exist in %s", p, commitID))
	}
	missing, err := r.repo.MissingChunks(entry.ChunkHashes)
	if err != nil {
		return err
	}
	scope := clientDomain.ChunkScope{Org: nu.Org, Project: nu.Project, CommitIDs: commitIDs(rev.commitID), Paths: []string{p}}
	if err := downloadMissing(ctx, r.diff.client, r.repo, scope, missing, entry.SizeBytes, progress...); err != nil {
		return err
	}
	prevMode := 0
	if tracked, ok, err := r.repo.SnapshotFile(p); err != nil {
		return err
	} else if ok {
		prevMode = tracked.Mode
	}
	file := materializedFile{Path: p, Mode: entry.Mode, SizeBytes: entry.SizeBytes, Encoding: entry.Encoding, FileHash: entry.Hash, ChunkHashes: entry.ChunkHashes}
	if err := materializeFile(root, file, prevMode, r.repo.OpenChunk); err != nil {
		return fmt.Errorf("restore %s: %w", p, err)
	}
	return nil
}

func (r *RevisionRestore) refusePending() error {
	merge, err := r.repo.LoadMergeState()
	if err != nil {
		return err
	}
	if merge != nil {
		return clientDomain.NewUserError("a merge is in progress; finish or abort it first")
	}
	revert, err := r.repo.LoadRevertState()
	if err != nil {
		return err
	}
	if revert != nil {
		return clientDomain.NewUserError("a revert is in progress; continue or abort it first")
	}
	return nil
}

func sparseCovers(prefixes []string, p string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if serverDomain.PrefixCovers(prefix, p) {
			return true
		}
	}
	return false
}
