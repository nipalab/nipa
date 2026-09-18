package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDiff "github.com/nipalab/nipa/internal/client/diff"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type diffClient interface {
	Connect(ctx context.Context, host string) error
	GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error)
	GetTreeNodeManifestByCommit(ctx context.Context, org, project string, commitID *snow.ID, commitHash *serverDomain.Hash) (*serverDomain.TreeNode, error)
	DownloadChunks(ctx context.Context, hashes []serverDomain.Hash, onChunk ...func(h serverDomain.Hash, data []byte)) (map[serverDomain.Hash][]byte, error)
}

type diffLocalRepo interface {
	Init(target string) error
	LoadConfig() (*clientDomain.Config, error)
	Snapshot() (*clientDomain.Snapshot, error)
	ListStaged() ([]string, error)
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	StoreChunk(hash serverDomain.Hash, data []byte) error
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
}

type Diff struct {
	auth      *Auth
	client    diffClient
	localRepo diffLocalRepo
}

func NewDiff(auth *Auth, client diffClient, localRepo diffLocalRepo) *Diff {
	return &Diff{auth: auth, client: client, localRepo: localRepo}
}

// DiffFile is one changed file with both contents loaded for rendering.
type DiffFile struct {
	Change clientDiff.Change
	// Old is the base content (nil for added files).
	Old []byte
	// New is the changed content (nil for deleted files).
	New []byte
	// OldUnavailable marks base content missing from the local cache
	// (e.g. subdirectory clones); the change is still reported.
	OldUnavailable bool
	// NewUnavailable marks new-side content missing from the local cache.
	NewUnavailable bool
}

type DiffResult struct {
	Base  string
	Head  string
	Files []DiffFile
}

// DiffOption customizes Diff.Run.
type DiffOption func(*diffConfig)

type diffConfig struct {
	// includeBinaryContent forces content loading for binary files.
	// Patch rendering never needs it (binaries render as one line), but
	// external diff tools do.
	includeBinaryContent bool
}

// WithBinaryContent loads binary file contents for external display.
func WithBinaryContent() DiffOption {
	return func(c *diffConfig) { c.includeBinaryContent = true }
}

// Run compares revisions or the working copy:
//
//	nipa diff           working tree vs last synced snapshot (offline)
//	nipa diff <rev>     revision vs working tree
//	nipa diff <a> <b>   revision vs revision
//
// A revision is a branch name, a commit ID (base36) or a commit hash (hex).
func (d *Diff) Run(ctx context.Context, root string, revs []string, opts ...DiffOption) (*DiffResult, error) {
	var dopts diffConfig
	for _, o := range opts {
		o(&dopts)
	}
	if err := d.localRepo.Init(root); err != nil {
		return nil, err
	}
	cfg, err := d.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	switch len(revs) {
	case 0:
		return d.workingDiff(root, cfg)
	case 1, 2:
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
			return d.workingVsRevision(ctx, nu, root, cfg, revs[0], dopts)
		}
		return d.revisionDiff(ctx, nu, revs[0], revs[1], dopts)
	default:
		return nil, clientDomain.NewUserError("too many revisions")
	}
}

func (d *Diff) workingDiff(root string, cfg *clientDomain.Config) (*DiffResult, error) {
	snapshot, err := d.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	stagedSet, err := d.stagedSet()
	if err != nil {
		return nil, err
	}
	oldMap := snapshotEntries(snapshot)
	newMap, contents, err := d.scanWorking(root, oldMap, stagedSet)
	if err != nil {
		return nil, err
	}

	changes := clientDiff.Compare(oldMap, newMap)
	oldContents := make(map[string][]byte)
	for _, c := range changes {
		if c.Status == clientDiff.Deleted || c.Status == clientDiff.Modified {
			if b, ok := d.loadCached(c.Old); ok {
				oldContents[c.Path] = b
			}
		}
	}
	return &DiffResult{
		Base:  cfg.Branch + " (last synced)",
		Head:  "working tree",
		Files: buildFiles(changes, oldContents, contents),
	}, nil
}

func (d *Diff) workingVsRevision(ctx context.Context, nu *clientDomain.NipaUrl, root string, cfg *clientDomain.Config, rev string, dopts diffConfig) (*DiffResult, error) {
	remote, err := d.resolveRevision(ctx, nu.Org, nu.Project, rev)
	if err != nil {
		return nil, err
	}
	oldMap := clientDiff.FromTree(remote)
	oldMap = scopeEntries(oldMap, nu.Path)
	stagedSet, err := d.stagedSet()
	if err != nil {
		return nil, err
	}
	newMap, contents, err := d.scanWorking(root, oldMap, stagedSet)
	if err != nil {
		return nil, err
	}

	changes := clientDiff.Compare(oldMap, newMap)
	if err := d.ensureContent(ctx, changes, true, false, dopts.includeBinaryContent); err != nil {
		return nil, err
	}
	oldContents := make(map[string][]byte)
	for _, c := range changes {
		if c.Status == clientDiff.Deleted || c.Status == clientDiff.Modified {
			if b, ok := d.loadCached(c.Old); ok {
				oldContents[c.Path] = b
			}
		}
	}
	return &DiffResult{
		Base:  rev,
		Head:  "working tree",
		Files: buildFiles(changes, oldContents, contents),
	}, nil
}

func (d *Diff) revisionDiff(ctx context.Context, nu *clientDomain.NipaUrl, revA, revB string, dopts diffConfig) (*DiffResult, error) {
	treeA, err := d.resolveRevision(ctx, nu.Org, nu.Project, revA)
	if err != nil {
		return nil, err
	}
	treeB, err := d.resolveRevision(ctx, nu.Org, nu.Project, revB)
	if err != nil {
		return nil, err
	}
	changes := clientDiff.Compare(
		scopeEntries(clientDiff.FromTree(treeA), nu.Path),
		scopeEntries(clientDiff.FromTree(treeB), nu.Path),
	)
	if err := d.ensureContent(ctx, changes, true, true, dopts.includeBinaryContent); err != nil {
		return nil, err
	}
	oldContents := make(map[string][]byte)
	newContents := make(map[string][]byte)
	for _, c := range changes {
		if c.Status == clientDiff.Deleted || c.Status == clientDiff.Modified {
			if b, ok := d.loadCached(c.Old); ok {
				oldContents[c.Path] = b
			}
		}
		if c.Status == clientDiff.Added || c.Status == clientDiff.Modified {
			if b, ok := d.loadCached(c.New); ok {
				newContents[c.Path] = b
			}
		}
	}
	return &DiffResult{
		Base:  revA,
		Head:  revB,
		Files: buildFiles(changes, oldContents, newContents),
	}, nil
}

// resolveRevision fetches the recursive manifest for a branch name, a
// commit ID (base36) or a commit hash (hex). A 64-character hex string is
// treated as a hash; anything else is tried as a branch first and falls
// back to a commit ID when the branch does not exist.
func (d *Diff) resolveRevision(ctx context.Context, org, project, rev string) (*serverDomain.TreeNode, error) {
	if hash, err := serverDomain.ParseHashHex(rev); err == nil {
		return d.client.GetTreeNodeManifestByCommit(ctx, org, project, nil, &hash)
	}
	tree, err := d.client.GetTreeNodeManifest(ctx, org, project, rev, "")
	if err == nil {
		return tree, nil
	}
	if !isNotFoundError(err) {
		return nil, err
	}
	id, err := snow.ParseBase36(rev)
	if err != nil {
		return nil, clientDomain.NewUserError(fmt.Sprintf("revision %q not found", rev))
	}
	return d.client.GetTreeNodeManifestByCommit(ctx, org, project, &id, nil)
}

func isNotFoundError(err error) bool {
	var domErr *clientDomain.Error
	if errors.As(err, &domErr) {
		return domErr.Code == 404
	}
	return false
}

func (d *Diff) ensureContent(ctx context.Context, changes []clientDiff.Change, old, new, includeBinary bool) error {
	set := make(map[serverDomain.Hash]struct{})
	var estimated int64
	for _, c := range changes {
		if !includeBinary && (c.Old.IsBinary || c.New.IsBinary) {
			continue
		}
		if old && (c.Status == clientDiff.Deleted || c.Status == clientDiff.Modified) {
			for _, h := range c.Old.ChunkHashes {
				set[h] = struct{}{}
			}
			estimated += c.Old.SizeBytes
		}
		if new && (c.Status == clientDiff.Added || c.Status == clientDiff.Modified) {
			for _, h := range c.New.ChunkHashes {
				set[h] = struct{}{}
			}
			estimated += c.New.SizeBytes
		}
	}
	if len(set) == 0 {
		return nil
	}
	wanted := make([]serverDomain.Hash, 0, len(set))
	for h := range set {
		wanted = append(wanted, h)
	}
	missing, err := d.localRepo.MissingChunks(wanted)
	if err != nil {
		return err
	}
	return downloadMissing(ctx, d.client, d.localRepo, missing, estimated)
}

func (d *Diff) scanWorking(root string, oldMap map[string]clientDiff.Entry, stagedSet map[string]bool) (map[string]clientDiff.Entry, map[string][]byte, error) {
	paths, err := walkWorkingFiles(root)
	if err != nil {
		return nil, nil, err
	}
	newMap := make(map[string]clientDiff.Entry, len(paths))
	contents := make(map[string][]byte, len(paths))
	for _, p := range paths {
		if _, tracked := oldMap[p]; !tracked && !stagedSet[p] {
			continue
		}
		fp := filepath.Join(root, filepath.FromSlash(p))
		data, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		hash, chunks, err := chunkFile(data)
		if err != nil {
			continue
		}
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		hashes := make([]serverDomain.Hash, len(chunks))
		sizes := make([]int64, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		newMap[p] = clientDiff.Entry{
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

func (d *Diff) loadCached(e clientDiff.Entry) ([]byte, bool) {
	b, err := clientDiff.LoadContent(d.localRepo.LoadChunk, e)
	if err != nil {
		return nil, false
	}
	return b, true
}

func buildFiles(changes []clientDiff.Change, oldContents, newContents map[string][]byte) []DiffFile {
	files := make([]DiffFile, 0, len(changes))
	for _, c := range changes {
		f := DiffFile{Change: c}
		if c.Status == clientDiff.Deleted || c.Status == clientDiff.Modified {
			if b, ok := oldContents[c.Path]; ok {
				f.Old = b
			} else {
				f.OldUnavailable = true
			}
		}
		if c.Status == clientDiff.Added || c.Status == clientDiff.Modified {
			if b, ok := newContents[c.Path]; ok {
				f.New = b
			} else {
				f.NewUnavailable = true
			}
		}
		files = append(files, f)
	}
	return files
}

func scopeEntries(m map[string]clientDiff.Entry, subpath string) map[string]clientDiff.Entry {
	subpath = strings.Trim(subpath, "/")
	if subpath == "" {
		return m
	}
	prefix := subpath + "/"
	out := make(map[string]clientDiff.Entry)
	for path, e := range m {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		e.Path = rest
		out[rest] = e
	}
	return out
}

func snapshotEntries(snapshot *clientDomain.Snapshot) map[string]clientDiff.Entry {
	out := make(map[string]clientDiff.Entry, len(snapshot.Files))
	for _, f := range snapshot.Files {
		mode := f.Mode
		if mode == 0 {
			mode = 2
		}
		hashes := make([]serverDomain.Hash, len(f.Chunks))
		sizes := make([]int64, len(f.Chunks))
		for i, c := range f.Chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		out[f.Path] = clientDiff.Entry{
			Path:        f.Path,
			Mode:        mode,
			SizeBytes:   f.SizeBytes,
			IsBinary:    f.IsBinary,
			Hash:        f.Hash,
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
	}
	return out
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
