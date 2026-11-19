package gitimport

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

const (
	ModeReadWrite  = 0o644
	ModeExecutable = 0o755
)

type SymlinkPolicy string

const (
	SymlinksSkip SymlinkPolicy = "skip"
	SymlinksFile SymlinkPolicy = "file"
)

func ParseSymlinkPolicy(s string) (SymlinkPolicy, error) {
	switch SymlinkPolicy(s) {
	case "", SymlinksSkip:
		return SymlinksSkip, nil
	case SymlinksFile:
		return SymlinksFile, nil
	}
	return "", fmt.Errorf("invalid symlink policy %q: want skip or file", s)
}

type Blob struct {
	FileHash   domain.Hash
	Chunks     []domain.Hash
	ChunkSizes []int64
	Encoding   string
	Size       int64
	IsBinary   bool
}

type BlobKey struct {
	SHA     string
	Profile string
}

type BlobCache interface {
	Get(key BlobKey) (*Blob, bool, error)
	Put(key BlobKey, blob *Blob) error
}

type MemoryBlobCache struct {
	mu    sync.Mutex
	blobs map[BlobKey]*Blob
}

func NewMemoryBlobCache() *MemoryBlobCache {
	return &MemoryBlobCache{blobs: map[BlobKey]*Blob{}}
}

func (m *MemoryBlobCache) Get(key BlobKey) (*Blob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[key]
	return b, ok, nil
}

func (m *MemoryBlobCache) Put(key BlobKey, blob *Blob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[key] = blob
	return nil
}

func (m *MemoryBlobCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.blobs)
}

const textProfile = "text"

func binaryProfile(path string) string {
	cfg := chunker.ConfigForFile(path, true)
	return fmt.Sprintf("bin-%d-%d-%d", cfg.Min, cfg.Avg, cfg.Max)
}

type ChunkSink func(chunker.EncodedChunk) error

type ConverterOptions struct {
	Symlinks SymlinkPolicy
	Cache    BlobCache
	Sink     ChunkSink
}

type File struct {
	domain.PushFile
	BlobSHA    string
	ChunkSizes []int64
}

type Author struct {
	Name  string
	Email string
	When  time.Time
}

type Event struct {
	Origin        string
	Parents       []string
	Author        Author
	CommitterWhen time.Time
	Message       string
	Files         []File
	Removed       []string
	Warnings      []Warning
}

func (e *Event) CreatedAt() time.Time {
	return e.Author.When
}

type InvalidPathsError struct {
	Commit string
	Paths  []string
}

func (e *InvalidPathsError) Error() string {
	return fmt.Sprintf("commit %s has paths Nipa cannot store: %s", e.Commit, strings.Join(e.Paths, ", "))
}

type Stats struct {
	BlobsRead int64
	BlobHits  int64
}

type Converter struct {
	src   *Source
	opts  ConverterOptions
	mu    sync.Mutex
	stats Stats
}

func NewConverter(src *Source, opts ConverterOptions) (*Converter, error) {
	policy, err := ParseSymlinkPolicy(string(opts.Symlinks))
	if err != nil {
		return nil, err
	}
	opts.Symlinks = policy
	if opts.Cache == nil {
		opts.Cache = NewMemoryBlobCache()
	}
	return &Converter{src: src, opts: opts}, nil
}

func (c *Converter) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *Converter) Convert(pc *PlannedCommit) (*Event, error) {
	commit, err := c.src.Commit(pc.Hash)
	if err != nil {
		return nil, fmt.Errorf("read commit %s: %w", pc.Hash, err)
	}
	d, err := c.delta(commit, pc)
	if err != nil {
		return nil, err
	}
	if len(d.invalid) > 0 {
		return nil, &InvalidPathsError{Commit: pc.Hash.String(), Paths: d.invalid}
	}
	ev := &Event{
		Origin:        pc.Origin,
		Parents:       append([]string(nil), pc.ParentOrigins...),
		Author:        Author{Name: commit.Author.Name, Email: commit.Author.Email, When: commit.Author.When},
		CommitterWhen: commit.Committer.When,
		Message:       commit.Message,
		Removed:       d.removed,
		Warnings:      d.warnings,
	}
	for _, f := range d.files {
		blob, err := c.convertBlob(f.path, f.blob)
		if err != nil {
			return nil, fmt.Errorf("convert %s in %s: %w", f.path, pc.Hash, err)
		}
		ev.Files = append(ev.Files, File{
			PushFile: domain.PushFile{
				Path:        f.path,
				Mode:        f.mode,
				SizeBytes:   blob.Size,
				IsBinary:    blob.IsBinary,
				Encoding:    blob.Encoding,
				FileHash:    blob.FileHash,
				ChunkHashes: append([]domain.Hash(nil), blob.Chunks...),
			},
			BlobSHA:    f.blob.String(),
			ChunkSizes: append([]int64(nil), blob.ChunkSizes...),
		})
	}
	return ev, nil
}

type deltaFile struct {
	path string
	mode int
	blob plumbing.Hash
}

type Submodule struct {
	Commit string
	Path   string
	URL    string
}

type delta struct {
	files      []deltaFile
	removed    []string
	warnings   []Warning
	invalid    []string
	symlinks   []string
	submodules []Submodule
}

func (c *Converter) delta(commit *object.Commit, pc *PlannedCommit) (*delta, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("read tree of %s: %w", pc.Hash, err)
	}
	base := &object.Tree{}
	if len(pc.Parents) > 0 {
		parent, err := c.src.Commit(pc.Parents[0])
		if err != nil {
			return nil, fmt.Errorf("read parent %s: %w", pc.Parents[0], err)
		}
		if base, err = parent.Tree(); err != nil {
			return nil, fmt.Errorf("read tree of %s: %w", pc.Parents[0], err)
		}
	}
	changes, err := object.DiffTree(base, tree)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", pc.Hash, err)
	}
	d := &delta{}
	var modules *config.Modules
	for _, ch := range changes {
		from, to := ch.From, ch.To
		_, fromKept := c.nipaMode(from)
		toMode, toKept := c.nipaMode(to)
		switch {
		case toKept:
			if domain.ValidateRepoPath(to.Name) != nil {
				d.invalid = append(d.invalid, to.Name)
				continue
			}
			d.files = append(d.files, deltaFile{path: to.Name, mode: toMode, blob: to.TreeEntry.Hash})
		case fromKept:
			if domain.ValidateRepoPath(from.Name) != nil {
				d.invalid = append(d.invalid, from.Name)
				continue
			}
			d.removed = append(d.removed, from.Name)
		}
		if to.Name == "" {
			continue
		}
		switch to.TreeEntry.Mode {
		case filemode.Symlink:
			d.symlinks = append(d.symlinks, to.Name)
			msg := "symlink skipped"
			if c.opts.Symlinks == SymlinksFile {
				msg = "symlink stored as a file holding its target"
			}
			d.warnings = append(d.warnings, Warning{Kind: WarnSymlink, Commit: pc.Hash.String(), Path: to.Name, Message: msg})
		case filemode.Submodule:
			if modules == nil {
				modules = readModules(tree)
			}
			sub := Submodule{Commit: pc.Hash.String(), Path: to.Name, URL: moduleURL(modules, to.Name)}
			d.submodules = append(d.submodules, sub)
			msg := "submodule skipped"
			if sub.URL != "" {
				msg += " (" + sub.URL + ")"
			}
			d.warnings = append(d.warnings, Warning{Kind: WarnSubmodule, Commit: pc.Hash.String(), Path: to.Name, Message: msg})
		}
	}
	sort.Strings(d.invalid)
	return d, nil
}

func (c *Converter) nipaMode(e object.ChangeEntry) (int, bool) {
	if e.Name == "" {
		return 0, false
	}
	switch e.TreeEntry.Mode {
	case filemode.Regular, filemode.Deprecated:
		return ModeReadWrite, true
	case filemode.Executable:
		return ModeExecutable, true
	case filemode.Symlink:
		return ModeReadWrite, c.opts.Symlinks == SymlinksFile
	}
	return 0, false
}

func readModules(tree *object.Tree) *config.Modules {
	modules := config.NewModules()
	f, err := tree.File(".gitmodules")
	if err != nil {
		return modules
	}
	content, err := f.Contents()
	if err != nil {
		return modules
	}
	_ = modules.Unmarshal([]byte(content))
	return modules
}

func moduleURL(modules *config.Modules, path string) string {
	for _, m := range modules.Submodules {
		if m.Path == path {
			return m.URL
		}
	}
	return ""
}

func (c *Converter) convertBlob(path string, h plumbing.Hash) (*Blob, error) {
	sha := h.String()
	for _, key := range []BlobKey{{SHA: sha, Profile: textProfile}, {SHA: sha, Profile: binaryProfile(path)}} {
		b, ok, err := c.opts.Cache.Get(key)
		if err != nil {
			return nil, err
		}
		if ok {
			c.count(func(s *Stats) { s.BlobHits++ })
			return b, nil
		}
	}
	gitBlob, err := c.src.repo.BlobObject(h)
	if err != nil {
		return nil, err
	}
	c.count(func(s *Stats) { s.BlobsRead++ })
	r := &blobReadSeeker{blob: gitBlob}
	defer func() { _ = r.Close() }()
	isBinary, err := chunker.ProbeBinary(r)
	if err != nil {
		return nil, err
	}
	b := &Blob{IsBinary: isBinary}
	b.Encoding, err = chunker.Encode(r, path, isBinary, "", func(ch chunker.EncodedChunk) error {
		b.Chunks = append(b.Chunks, ch.Hash)
		b.ChunkSizes = append(b.ChunkSizes, ch.SizeBytes)
		b.Size += ch.RawSize
		if c.opts.Sink != nil {
			return c.opts.Sink(ch)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if b.Size != gitBlob.Size {
		return nil, fmt.Errorf("blob %s: read %d bytes, want %d", sha, b.Size, gitBlob.Size)
	}
	b.FileHash = chunker.FileHash(b.Chunks)
	key := BlobKey{SHA: sha, Profile: textProfile}
	if isBinary {
		key.Profile = binaryProfile(path)
	}
	if err := c.opts.Cache.Put(key, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (c *Converter) count(fn func(*Stats)) {
	c.mu.Lock()
	fn(&c.stats)
	c.mu.Unlock()
}

var errBlobSeek = errors.New("blob reader only rewinds to the start")

type blobReadSeeker struct {
	blob *object.Blob
	rc   io.ReadCloser
}

func (r *blobReadSeeker) Read(p []byte) (int, error) {
	if r.rc == nil {
		rc, err := r.blob.Reader()
		if err != nil {
			return 0, err
		}
		r.rc = rc
	}
	return r.rc.Read(p)
}

func (r *blobReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != io.SeekStart {
		return 0, errBlobSeek
	}
	return 0, r.Close()
}

func (r *blobReadSeeker) Close() error {
	if r.rc == nil {
		return nil
	}
	err := r.rc.Close()
	r.rc = nil
	return err
}
