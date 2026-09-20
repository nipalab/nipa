package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type diffClient interface {
	Connect(ctx context.Context, host string) error
	GetBranchByName(ctx context.Context, org, project, name string) (*serverDomain.Branch, error)
	GetCommit(ctx context.Context, org, project, commitID string) (*clientDomain.CommitDetail, error)
	GetMergeBase(ctx context.Context, org, project string, target, source clientDomain.MergeRef) (*clientDomain.MergeBaseInfo, error)
	DownloadChunks(ctx context.Context, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error
}

type diffLocalRepo interface {
	Init(target string) error
	LoadConfig() (*clientDomain.Config, error)
	LoadCommit() (*clientDomain.LocalCommit, error)
	Snapshot() (*clientDomain.Snapshot, error)
	ListStaged() ([]string, error)
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	StoreChunks(chunks []*serverDomain.ChunkData) error
	OpenChunk(hash serverDomain.Hash) (io.ReadCloser, error)
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
}

// DiffOptions controls which paths and states are compared.
type DiffOptions struct {
	// Staged only compares paths staged for the next push.
	Staged bool
	// Paths limits the diff to these repo-relative paths (prefix match).
	Paths []string
	// MergeBase compares the merge base of two revisions against the second.
	MergeBase bool
	// Binary loads binary content too (external diff tools).
	Binary bool
}

type Diff struct {
	auth      *Auth
	client    diffClient
	localRepo diffLocalRepo
}

func NewDiff(auth *Auth, client diffClient, localRepo diffLocalRepo) *Diff {
	return &Diff{auth: auth, client: client, localRepo: localRepo}
}

type revision struct {
	token    string
	commitID string
	entries  map[string]diff.Entry
}

// Run compares the working tree against the last synced snapshot (no
// revisions, offline), a revision against the working tree (one revision), or
// two revisions against each other.
func (d *Diff) Run(ctx context.Context, root string, revs []string, opts DiffOptions) ([]diff.FileDiff, error) {
	if err := d.localRepo.Init(root); err != nil {
		return nil, err
	}
	cfg, err := d.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	return d.dispatch(ctx, root, revs, cfg, opts)
}

func (d *Diff) dispatch(ctx context.Context, root string, revs []string, cfg *clientDomain.Config, opts DiffOptions) ([]diff.FileDiff, error) {
	if len(revs) == 0 {
		if opts.MergeBase {
			return nil, clientDomain.NewUserError("--merge-base requires two revisions")
		}
		return d.workingDiff(root, opts)
	}
	if len(revs) > 2 {
		return nil, clientDomain.NewUserError("too many revisions (expected at most two)")
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}
	if err := d.client.Connect(ctx, nu.Host); err != nil {
		return nil, err
	}
	if err := d.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return nil, err
	}
	if len(revs) == 1 {
		if opts.MergeBase {
			return nil, clientDomain.NewUserError("--merge-base requires two revisions")
		}
		return d.workingVsRevision(ctx, nu, root, cfg, revs[0], opts)
	}
	return d.revisionDiff(ctx, nu, cfg, revs[0], revs[1], opts)
}

func (d *Diff) workingDiff(root string, opts DiffOptions) ([]diff.FileDiff, error) {
	snapshot, err := d.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := d.stagedSet()
	if err != nil {
		return nil, err
	}
	oldMap := snapshotEntries(snapshot, opts, staged)
	newMap, contents, err := d.scanWorking(root, oldMap, staged, opts)
	if err != nil {
		return nil, err
	}
	changes := diff.Compare(oldMap, newMap)
	changes = diff.DetectRenames(changes, d.similarityLoader(contents))
	return diff.AttachContents(changes, d.attachLoader(contents, opts.Binary)), nil
}

func (d *Diff) workingVsRevision(ctx context.Context, nu *clientDomain.NipaUrl, root string, cfg *clientDomain.Config, token string, opts DiffOptions) ([]diff.FileDiff, error) {
	base, err := d.resolveRevision(ctx, nu, cfg, token)
	if err != nil {
		return nil, err
	}
	staged, err := d.stagedSet()
	if err != nil {
		return nil, err
	}
	newMap, contents, err := d.scanWorking(root, base.entries, staged, opts)
	if err != nil {
		return nil, err
	}
	changes := diff.Compare(base.entries, newMap)
	if err := d.ensureContent(ctx, changes, true, false, opts.Binary); err != nil {
		return nil, err
	}
	changes = diff.DetectRenames(changes, d.similarityLoader(contents))
	return diff.AttachContents(changes, d.attachLoader(contents, opts.Binary)), nil
}

func (d *Diff) revisionDiff(ctx context.Context, nu *clientDomain.NipaUrl, cfg *clientDomain.Config, revA, revB string, opts DiffOptions) ([]diff.FileDiff, error) {
	base, err := d.resolveRevision(ctx, nu, cfg, revA)
	if err != nil {
		return nil, err
	}
	head, err := d.resolveRevision(ctx, nu, cfg, revB)
	if err != nil {
		return nil, err
	}
	baseEntries := base.entries
	if opts.MergeBase {
		baseEntries, err = d.mergeBaseEntries(ctx, nu, base, head)
		if err != nil {
			return nil, err
		}
	}
	changes := diff.Compare(baseEntries, head.entries)
	if err := d.ensureContent(ctx, changes, true, true, opts.Binary); err != nil {
		return nil, err
	}
	changes = diff.DetectRenames(changes, d.similarityLoader(nil))
	return diff.AttachContents(changes, d.attachLoader(nil, opts.Binary)), nil
}

// similarityLoader loads text content for rename detection, preferring the
// working-tree contents map over the local object cache. Binary files never
// load content; similarity falls back to chunk overlap.
func (d *Diff) similarityLoader(contents map[string][]byte) func(diff.Entry) ([]byte, bool) {
	return func(e diff.Entry) ([]byte, bool) {
		if e.IsBinary {
			return nil, false
		}
		if contents != nil {
			if content, ok := contents[e.Path]; ok {
				return content, true
			}
		}
		content, err := diff.LoadContent(d.localRepo.LoadChunk, e)
		if err != nil {
			return nil, false
		}
		return content, true
	}
}

// attachLoader loads content for rendering. The working-tree contents map is
// only used for the new side; the old side always comes from the object cache.
// Binary content is skipped unless loadBinary is set; skipped binaries stay
// available as metadata.
func (d *Diff) attachLoader(contents map[string][]byte, loadBinary bool) func(diff.Entry, bool) ([]byte, bool) {
	return func(e diff.Entry, isNew bool) ([]byte, bool) {
		if isNew && contents != nil {
			if content, ok := contents[e.Path]; ok {
				return content, true
			}
		}
		if e.IsBinary && !loadBinary {
			return nil, true
		}
		content, err := diff.LoadContent(d.localRepo.LoadChunk, e)
		if err != nil {
			return nil, false
		}
		return content, true
	}
}

// resolveRevision maps a branch name, a base36 commit ID, or HEAD/@ (the
// locally pinned commit, falling back to the configured branch) to its tree.
func (d *Diff) resolveRevision(ctx context.Context, nu *clientDomain.NipaUrl, cfg *clientDomain.Config, token string) (*revision, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, clientDomain.NewUserError("a revision is required")
	}
	if token == "HEAD" || token == "@" {
		pin, err := d.localRepo.LoadCommit()
		if err != nil {
			return nil, err
		}
		if pin != nil && pin.CommitID != "" {
			return d.revisionByCommit(ctx, nu, pin.CommitID)
		}
		token = cfg.Branch
	}

	branch, err := d.client.GetBranchByName(ctx, nu.Org, nu.Project, token)
	if err == nil {
		if branch == nil || branch.CommitID == nil {
			return &revision{token: token, entries: map[string]diff.Entry{}}, nil
		}
		return d.revisionByCommit(ctx, nu, branch.CommitID.Base36())
	}
	if !isNotFoundError(err) {
		return nil, err
	}
	if _, perr := snow.ParseBase36(token); perr != nil {
		return nil, clientDomain.NewUserError(fmt.Sprintf("revision %q not found", token))
	}
	rev, err := d.revisionByCommit(ctx, nu, token)
	if err != nil {
		if isNotFoundError(err) {
			return nil, clientDomain.NewUserError(fmt.Sprintf("revision %q not found", token))
		}
		return nil, err
	}
	return rev, nil
}

func (d *Diff) revisionByCommit(ctx context.Context, nu *clientDomain.NipaUrl, commitID string) (*revision, error) {
	detail, err := d.client.GetCommit(ctx, nu.Org, nu.Project, commitID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, clientDomain.NewUserError(fmt.Sprintf("revision %q not found", commitID))
	}
	return &revision{token: commitID, commitID: detail.ID, entries: diff.FromTree(detail.Tree)}, nil
}

func (d *Diff) mergeBaseEntries(ctx context.Context, nu *clientDomain.NipaUrl, base, head *revision) (map[string]diff.Entry, error) {
	if base.commitID == "" || head.commitID == "" {
		return map[string]diff.Entry{}, nil
	}
	info, err := d.client.GetMergeBase(ctx, nu.Org, nu.Project,
		clientDomain.MergeRef{CommitID: base.commitID},
		clientDomain.MergeRef{CommitID: head.commitID})
	if err != nil {
		return nil, err
	}
	if info == nil || info.MergeBaseTree == nil {
		return map[string]diff.Entry{}, nil
	}
	return diff.FromTree(info.MergeBaseTree), nil
}

// ensureContent downloads the chunks needed to render the given changes.
// Binary content is skipped unless loadBinary is set.
func (d *Diff) ensureContent(ctx context.Context, changes []diff.Change, needOld, needNew, loadBinary bool) error {
	set := make(map[serverDomain.Hash]struct{})
	var estimated int64
	for _, c := range changes {
		if !loadBinary && (c.Old.IsBinary || c.New.IsBinary) {
			continue
		}
		if needOld && (c.Status == diff.Deleted || c.Status == diff.Modified || c.Status == diff.Renamed) {
			for _, h := range c.Old.ChunkHashes {
				set[h] = struct{}{}
			}
			estimated += c.Old.SizeBytes
		}
		if needNew && (c.Status == diff.Added || c.Status == diff.Modified || c.Status == diff.Renamed) {
			for _, h := range c.New.ChunkHashes {
				set[h] = struct{}{}
			}
			estimated += c.New.SizeBytes
		}
	}
	if len(set) == 0 {
		return nil
	}
	hashes := make([]serverDomain.Hash, 0, len(set))
	for h := range set {
		hashes = append(hashes, h)
	}
	missing, err := d.localRepo.MissingChunks(hashes)
	if err != nil {
		return err
	}
	return downloadMissing(ctx, d.client, d.localRepo, missing, estimated)
}

func (d *Diff) stagedSet() (map[string]bool, error) {
	staged, err := d.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(staged))
	for _, p := range staged {
		set[p] = true
	}
	return set, nil
}

func isNotFoundError(err error) bool {
	var domErr *clientDomain.Error
	return errors.As(err, &domErr) && domErr.Code == 404
}

func (d *Diff) scanWorking(root string, oldMap map[string]diff.Entry, staged map[string]bool, opts DiffOptions) (map[string]diff.Entry, map[string][]byte, error) {
	paths, err := walkWorkingFiles(root)
	if err != nil {
		return nil, nil, err
	}
	newMap := make(map[string]diff.Entry, len(paths))
	contents := make(map[string][]byte, len(paths))
	for _, p := range paths {
		if !pathMatches(p, opts.Paths) {
			continue
		}
		if opts.Staged && !staged[p] {
			continue
		}
		if _, tracked := oldMap[p]; !tracked && !staged[p] {
			continue
		}
		fp := filepath.Join(root, filepath.FromSlash(p))
		data, err := os.ReadFile(fp)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, fmt.Errorf("read %s: %w", p, err)
		}
		info, err := os.Stat(fp)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, fmt.Errorf("stat %s: %w", p, err)
		}
		hash, chunks, err := chunkFile(data)
		if err != nil {
			return nil, nil, fmt.Errorf("chunk %s: %w", p, err)
		}
		hashes := make([]serverDomain.Hash, len(chunks))
		sizes := make([]int64, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		newMap[p] = diff.Entry{
			Path:        p,
			Mode:        serverModeFromPerm(info.Mode()),
			SizeBytes:   int64(len(data)),
			IsBinary:    chunker.IsBinary(data),
			Hash:        hash,
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
		contents[p] = data
	}
	return newMap, contents, nil
}

func snapshotEntries(snapshot *clientDomain.Snapshot, opts DiffOptions, staged map[string]bool) map[string]diff.Entry {
	out := make(map[string]diff.Entry, len(snapshot.Files))
	for _, f := range snapshot.Files {
		if opts.Staged && !staged[f.Path] {
			continue
		}
		if !pathMatches(f.Path, opts.Paths) {
			continue
		}
		mode := f.Mode
		if mode == 0 {
			mode = 2
		}
		out[f.Path] = diff.Entry{
			Path:        f.Path,
			Mode:        mode,
			SizeBytes:   f.SizeBytes,
			IsBinary:    f.IsBinary,
			Hash:        f.Hash,
			ChunkHashes: f.Chunks,
		}
	}
	return out
}

func pathMatches(path string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		f = strings.Trim(strings.TrimPrefix(filepath.ToSlash(f), "./"), "/")
		if f == "" || path == f || strings.HasPrefix(path, f+"/") {
			return true
		}
	}
	return false
}

func serverModeFromPerm(perm os.FileMode) int {
	if perm&0o111 != 0 {
		return 3
	}
	if perm&0o222 == 0 {
		return 1
	}
	return 2
}
