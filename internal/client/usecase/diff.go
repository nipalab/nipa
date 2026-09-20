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
	// Filter limits changes to the given statuses; nil keeps all.
	Filter map[diff.Status]bool
	// Reverse swaps the old and new sides.
	Reverse bool
	// MergeBase compares the merge base of two revisions against the second.
	MergeBase bool
	// Text loads binary content too, for text rendering of binary files.
	Text bool
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
	changes := filterStatus(diff.Compare(oldMap, newMap), opts.Filter)
	return d.buildFiles(changes, contents, opts), nil
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
	changes := filterStatus(diff.Compare(base.entries, newMap), opts.Filter)
	if err := d.ensureContent(ctx, changes, true, false, opts.Text); err != nil {
		return nil, err
	}
	return d.buildFiles(changes, contents, opts), nil
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
	changes := filterStatus(diff.Compare(baseEntries, head.entries), opts.Filter)
	if err := d.ensureContent(ctx, changes, true, true, opts.Text); err != nil {
		return nil, err
	}
	return d.buildFiles(changes, nil, opts), nil
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
// Binary content is skipped unless text rendering was requested.
func (d *Diff) ensureContent(ctx context.Context, changes []diff.Change, needOld, needNew, text bool) error {
	set := make(map[serverDomain.Hash]struct{})
	var estimated int64
	for _, c := range changes {
		if !text && (c.Old.IsBinary || c.New.IsBinary) {
			continue
		}
		if needOld && (c.Status == diff.Deleted || c.Status == diff.Modified) {
			for _, h := range c.Old.ChunkHashes {
				set[h] = struct{}{}
			}
			estimated += c.Old.SizeBytes
		}
		if needNew && (c.Status == diff.Added || c.Status == diff.Modified) {
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

func (d *Diff) buildFiles(changes []diff.Change, contents map[string][]byte, opts DiffOptions) []diff.FileDiff {
	files := make([]diff.FileDiff, 0, len(changes))
	for _, c := range changes {
		f := diff.FileDiff{Change: c}
		switch c.Status {
		case diff.Added:
			f.New, f.NewUnavailable = d.newContent(c.New, contents, opts.Text)
		case diff.Deleted:
			f.Old, f.OldUnavailable = d.loadOld(c.Old, opts.Text)
		case diff.Modified:
			f.Old, f.OldUnavailable = d.loadOld(c.Old, opts.Text)
			f.New, f.NewUnavailable = d.newContent(c.New, contents, opts.Text)
		}
		files = append(files, f)
	}
	if opts.Reverse {
		reverseFiles(files)
	}
	return files
}

func (d *Diff) newContent(e diff.Entry, contents map[string][]byte, text bool) ([]byte, bool) {
	if contents != nil {
		return contents[e.Path], false
	}
	if e.IsBinary && !text {
		return nil, false
	}
	content, err := diff.LoadContent(d.localRepo.LoadChunk, e)
	if err != nil {
		return nil, true
	}
	return content, false
}

func (d *Diff) loadOld(e diff.Entry, text bool) ([]byte, bool) {
	if e.IsBinary && !text {
		return nil, false
	}
	content, err := diff.LoadContent(d.localRepo.LoadChunk, e)
	if err != nil {
		return nil, true
	}
	return content, false
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
			continue
		}
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		hash, chunks, err := chunkFile(data)
		if err != nil {
			continue
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

func filterStatus(changes []diff.Change, filter map[diff.Status]bool) []diff.Change {
	if filter == nil {
		return changes
	}
	out := make([]diff.Change, 0, len(changes))
	for _, c := range changes {
		if filter[c.Status] {
			out = append(out, c)
		}
	}
	return out
}

func reverseFiles(files []diff.FileDiff) {
	for i := range files {
		f := &files[i]
		f.Change.Old, f.Change.New = f.Change.New, f.Change.Old
		f.Old, f.New = f.New, f.Old
		f.OldUnavailable, f.NewUnavailable = f.NewUnavailable, f.OldUnavailable
		switch f.Change.Status {
		case diff.Added:
			f.Change.Status = diff.Deleted
		case diff.Deleted:
			f.Change.Status = diff.Added
		}
	}
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
