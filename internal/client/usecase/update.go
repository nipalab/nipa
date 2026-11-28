package usecase

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
)

type updateClient interface {
	Connect(ctx context.Context, host string) error
	GetBranchByName(ctx context.Context, org, project, name string) (*domain.Branch, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*domain.TreeNode, error)
	GetCommitTreeManifest(ctx context.Context, org, project, commitID string, paths []string) (*domain.TreeNode, error)
	GetTagByName(ctx context.Context, org, project, name string) (*clientDomain.Tag, error)
	DownloadChunks(ctx context.Context, scope clientDomain.ChunkScope, hashes []domain.Hash, onChunk func(h domain.Hash, data []byte) error) error
}

type chunkDownloader interface {
	DownloadChunks(ctx context.Context, scope clientDomain.ChunkScope, hashes []domain.Hash, onChunk func(h domain.Hash, data []byte) error) error
}

type DownloadProgress interface {
	DownloadStart(objects int, estimatedBytes int64)
	DownloadProgress(objectsDone int, bytesDone int64)
	DownloadEnd()
}

type UploadProgress interface {
	UploadStart(objects int, totalBytes int64)
	UploadProgress(objectsDone int, bytesDone int64)
	UploadEnd()
}

// UploadVerifyProgress is implemented by progress sinks that want to show the
// server-side verification that follows the transfer of every upload window.
type UploadVerifyProgress interface {
	UploadVerify(objects int)
}

type workingCopyLocalRepo interface {
	Snapshot() (*clientDomain.Snapshot, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunks(chunks []*domain.ChunkData) error
	OpenChunk(hash domain.Hash) (io.ReadCloser, error)
	LoadChunk(hash domain.Hash) ([]byte, error)
	SaveStatEntries(entries map[string]clientDomain.StatEntry) error
}

type updateLocalRepo interface {
	Init(target string) error
	LoadConfig() (*clientDomain.Config, error)
	SaveConfig(cfg clientDomain.Config) error
	ListStaged() ([]string, error)
	LoadCommit() (*clientDomain.LocalCommit, error)
	SaveCommit(commitID, commitHash string) error
	Snapshot() (*clientDomain.Snapshot, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunks(chunks []*domain.ChunkData) error
	OpenChunk(hash domain.Hash) (io.ReadCloser, error)
	LoadChunk(hash domain.Hash) ([]byte, error)
	SaveStatEntries(entries map[string]clientDomain.StatEntry) error
	SaveSyncedTree(root *domain.TreeNode, commitID string, sparse []string) error
	LoadSyncedTree() (*clientDomain.SyncedTree, error)
}

// UpdateResult says what an update did.
type UpdateResult struct {
	// UpToDate is set when the working copy already held the target commit
	// with the configured sparse set, so nothing was fetched or written.
	UpToDate bool
}

type Update struct {
	auth      *Auth
	client    updateClient
	localRepo updateLocalRepo
	force     bool
}

// WithForce returns an Update whose syncs replace locally changed files
// (force) or refuse to (the default): without it Run, Switch and SwitchTag
// stop before writing anything when a file they would replace holds unpushed
// edits, or an untracked file is in the way.
func (u *Update) WithForce(force bool) *Update {
	forced := *u
	forced.force = force
	return &forced
}

func (u *Update) sync(ctx context.Context, root string, tree *domain.TreeNode, scope clientDomain.ChunkScope, progress ...DownloadProgress) error {
	return syncWorkingCopyOpts(ctx, u.client, u.localRepo, root, tree, scope, syncOptions{keepLocalChanges: !u.force}, progress...)
}

func NewUpdate(auth *Auth, client updateClient, localRepo updateLocalRepo) *Update {
	return &Update{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
	}
}

// Run is RunResult for callers that do not report what the update did.
func (u *Update) Run(ctx context.Context, root string, progress ...DownloadProgress) error {
	_, err := u.RunResult(ctx, root, progress...)
	return err
}

// RunResult brings the working copy to the branch head (or, detached, to the
// checked-out commit). When the stored tree already is that commit's tree,
// fetched with the configured sparse set, and the update is not forced, it
// returns UpToDate without fetching the manifest, scanning the working copy
// or pruning the cache: a tracked file deleted from disk is then not restored,
// which is what a forced update is for.
func (u *Update) RunResult(ctx context.Context, root string, progress ...DownloadProgress) (result UpdateResult, err error) {
	ctx, span := obs.StartSpan(ctx, "nipa.update")
	defer func() { obs.FinishSpan(span, err) }()
	var effect *pushCacheEffect
	defer func() {
		if !result.UpToDate {
			pruneAfterPush(u.localRepo, effect, err)
		}
	}()

	if err := u.localRepo.Init(root); err != nil {
		return result, err
	}
	unlock, err := lockShared(u.localRepo)
	if err != nil {
		return result, err
	}
	defer unlock()
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return result, err
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return result, err
	}
	span.SetAttributes(
		attribute.String(obs.AttrOrg, nu.Org),
		attribute.String(obs.AttrProject, nu.Project),
		attribute.String(obs.AttrBranch, cfg.Branch),
	)
	if err := u.client.Connect(ctx, nu.Origin); err != nil {
		return result, err
	}
	if err := u.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return result, err
	}
	if cfg.Head != nil {
		result, effect, err = u.runDetached(ctx, root, cfg, nu, progress...)
		return result, err
	}

	headCommitID, err := u.branchHead(ctx, nu, cfg.Branch)
	if err != nil {
		return result, err
	}
	if headCommitID != "" && !u.force {
		localCommit, err := u.localRepo.LoadCommit()
		if err != nil {
			return result, err
		}
		if localCommit != nil && localCommit.CommitID == headCommitID {
			upToDate, err := u.syncedAt(headCommitID, cfg.Sparse)
			if err != nil {
				return result, err
			}
			if upToDate {
				span.SetAttributes(attribute.Bool("nipa.update.up_to_date", true))
				return UpdateResult{UpToDate: true}, nil
			}
		}
	}
	effect, err = u.syncTo(ctx, root, nu, cfg.Sparse, syncGoal{commitID: headCommitID, branch: cfg.Branch}, progress...)
	if err != nil {
		return result, err
	}
	if headCommitID == "" {
		return result, nil
	}
	return result, u.localRepo.SaveCommit(headCommitID, "")
}

// syncedAt reports whether the stored tree is recorded as commitID's tree
// fetched with the sparse set. No record means unknown, so false.
func (u *Update) syncedAt(commitID string, sparse []string) (bool, error) {
	synced, err := u.localRepo.LoadSyncedTree()
	if err != nil || synced == nil {
		return false, err
	}
	return synced.CommitID == commitID && sameSparseSet(synced.Sparse, sparse), nil
}

// sameSparseSet compares two sparse sets ignoring order, duplicates and empty
// entries; two empty sets (full checkouts) are equal.
func sameSparseSet(a, b []string) bool {
	canonical := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			if p != "" {
				out = append(out, p)
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(canonical(a), canonical(b))
}

func (u *Update) runDetached(ctx context.Context, root string, cfg *clientDomain.Config, nu *clientDomain.NipaUrl, progress ...DownloadProgress) (UpdateResult, *pushCacheEffect, error) {
	var result UpdateResult
	localCommit, err := u.localRepo.LoadCommit()
	if err != nil {
		return result, nil, err
	}
	if localCommit == nil || localCommit.CommitID == "" {
		return result, nil, clientDomain.NewUserError("no commit checked out; run nipa switch to a branch first")
	}
	if !u.force {
		// A commit's tree never changes, so the checked-out commit synced with
		// the same sparse set leaves nothing to fetch.
		upToDate, err := u.syncedAt(localCommit.CommitID, cfg.Sparse)
		if err != nil {
			return result, nil, err
		}
		if upToDate {
			return UpdateResult{UpToDate: true}, nil, nil
		}
	}
	effect, err := u.syncTo(ctx, root, nu, cfg.Sparse, syncGoal{commitID: localCommit.CommitID, detached: true}, progress...)
	if err != nil {
		return result, nil, err
	}
	return result, effect, u.localRepo.SaveCommit(localCommit.CommitID, localCommit.CommitHash)
}

func (u *Update) branchHead(ctx context.Context, nu *clientDomain.NipaUrl, branch string) (string, error) {
	info, err := u.client.GetBranchByName(ctx, nu.Org, nu.Project, branch)
	if err != nil {
		return "", err
	}
	if info == nil || info.CommitID == nil {
		return "", nil
	}
	return info.CommitID.Base36(), nil
}

func commitIDs(ids ...string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// SetSparse persists the sparse path prefixes for the working copy. An empty
// set means a full checkout.
func (u *Update) SetSparse(root string, paths []string) error {
	if err := u.localRepo.Init(root); err != nil {
		return err
	}
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	normalized, err := normalizeSparsePaths(paths)
	if err != nil {
		return err
	}
	cfg.Sparse = normalized
	return u.localRepo.SaveConfig(*cfg)
}

func normalizeSparsePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		normalized, err := domain.NormalizePathPrefix(path)
		if err != nil {
			return nil, clientDomain.NewUserError(err.Error())
		}
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func (u *Update) Switch(ctx context.Context, root, branch string, progress ...DownloadProgress) (err error) {
	ctx, span := obs.StartSpan(ctx, "nipa.switch",
		attribute.String(obs.AttrBranch, branch))
	defer func() { obs.FinishSpan(span, err) }()
	var effect *pushCacheEffect
	defer func() { pruneAfterPush(u.localRepo, effect, err) }()

	if err := u.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(u.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(branch) == "" {
		return clientDomain.NewUserError("branch name is required")
	}
	if branch == cfg.Branch && cfg.Head == nil {
		return nil
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	staged, err := u.localRepo.ListStaged()
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		return clientDomain.NewUserError("cannot switch branches with staged changes; push or reset them first")
	}
	if err := u.client.Connect(ctx, nu.Origin); err != nil {
		return err
	}
	if err := u.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}

	headCommitID, err := u.branchHead(ctx, nu, branch)
	if err != nil {
		return err
	}
	effect, err = u.syncTo(ctx, root, nu, cfg.Sparse, syncGoal{commitID: headCommitID, branch: branch}, progress...)
	if err != nil {
		return err
	}
	if err := u.localRepo.SaveConfig(clientDomain.Config{Url: cfg.Url, Branch: branch, Sparse: cfg.Sparse}); err != nil {
		return err
	}
	if headCommitID == "" {
		return nil
	}
	return u.localRepo.SaveCommit(headCommitID, "")
}

func (u *Update) SwitchTag(ctx context.Context, root, tagName string, progress ...DownloadProgress) (err error) {
	ctx, span := obs.StartSpan(ctx, "nipa.switch",
		attribute.String("nipa.tag", tagName))
	defer func() { obs.FinishSpan(span, err) }()
	var effect *pushCacheEffect
	defer func() { pruneAfterPush(u.localRepo, effect, err) }()

	if err := u.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(u.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	tagName = strings.TrimSpace(tagName)
	if tagName == "" {
		return clientDomain.NewUserError("tag name is required")
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	staged, err := u.localRepo.ListStaged()
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		return clientDomain.NewUserError("cannot switch to a tag with staged changes; push or reset them first")
	}
	if err := u.client.Connect(ctx, nu.Origin); err != nil {
		return err
	}
	if err := u.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}

	tag, err := u.client.GetTagByName(ctx, nu.Org, nu.Project, tagName)
	if err != nil {
		return err
	}
	effect, err = u.syncTo(ctx, root, nu, cfg.Sparse, syncGoal{commitID: tag.CommitID, detached: true}, progress...)
	if err != nil {
		return err
	}
	cfg.Head = &clientDomain.HeadRef{Kind: clientDomain.HeadKindTag, Name: tag.Name}
	if err := u.localRepo.SaveConfig(*cfg); err != nil {
		return err
	}
	return u.localRepo.SaveCommit(tag.CommitID, "")
}

// syncedTreeClearer is implemented by repositories that keep a synced-tree
// record (the update early exit's evidence that the working copy holds a
// commit's tree).
type syncedTreeClearer interface {
	ClearSyncedTree() error
}

// clearSyncedTree drops the synced-tree record before a sync writes to the
// working copy, so a sync that fails or is killed part way leaves no record
// claiming the working copy is complete; the caller saves a new one once the
// sync succeeded.
func clearSyncedTree(lr any) error {
	if c, ok := lr.(syncedTreeClearer); ok {
		return c.ClearSyncedTree()
	}
	return nil
}

type syncOptions struct {
	// keepLocalChanges refuses the sync, before anything is written, when it
	// would replace a locally changed or untracked working file.
	keepLocalChanges bool
	// restoreMissing makes a delta sync also write back every tracked file
	// that is gone from disk, which the full sync always does.
	restoreMissing bool
}

// syncWorkingCopy brings the working copy to tree, replacing whatever is in
// the way.
func syncWorkingCopy(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, root string, tree *domain.TreeNode, scope clientDomain.ChunkScope, progress ...DownloadProgress) error {
	return syncWorkingCopyOpts(ctx, client, lr, root, tree, scope, syncOptions{}, progress...)
}

func syncWorkingCopyOpts(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, root string, tree *domain.TreeNode, scope clientDomain.ChunkScope, opts syncOptions, progress ...DownloadProgress) (err error) {
	ctx, span := obs.StartSpan(ctx, "nipa.sync",
		attribute.String(obs.AttrOrg, scope.Org),
		attribute.String(obs.AttrProject, scope.Project))
	defer func() { obs.FinishSpan(span, err) }()

	if err := validateSyncTree(tree); err != nil {
		return err
	}
	endWrite, err := beginWorkingWrite(lr, root)
	if err != nil {
		return err
	}
	defer endWrite()
	base, err := lr.Snapshot()
	if err != nil {
		return err
	}
	baseByPath := make(map[string]clientDomain.SnapshotFile, len(base.Files))
	for _, f := range base.Files {
		baseByPath[f.Path] = f
	}

	pins, err := loadPins(lr)
	if err != nil {
		return err
	}

	// Only the files this sync rewrites get their chunk lists copied out of
	// the tree; an unchanged file costs one hash over its chunk hashes.
	plan := newSyncPlan(pins, opts)
	present := make(map[string]struct{}, len(baseByPath))
	walkTreeFiles(tree, "", func(path string, file *domain.File) {
		present[path] = struct{}{}
		base, tracked := baseByPath[path]
		if base.Hash == treeFileHash(file) && base.Mode == file.Mode && fileExists(root, path) {
			// The file was not rewritten and its content was not verified, so
			// leave any existing fingerprint untouched and let status hash-verify
			// it once if it has none.
			return
		}
		plan.write(materializedFromTree(path, file), base, tracked)
	})
	for path, base := range baseByPath {
		if _, kept := present[path]; !kept {
			plan.removals = append(plan.removals, base)
		}
	}
	return plan.run(ctx, client, lr, root, scope, progress...)
}

// syncPlan is what a sync does to the working copy: the files it writes and
// the tracked files it removes.
type syncPlan struct {
	pins []string
	opts syncOptions
	// jobs are written from the object cache, directJobs straight into their
	// working files; jobFiles lists both.
	jobs, directJobs []materializeJob
	jobFiles         []materializedFile
	want             []domain.Hash
	replaced         []overwriteCandidate
	cached           map[domain.Hash]struct{}
	removals         []clientDomain.SnapshotFile
}

func newSyncPlan(pins []string, opts syncOptions) *syncPlan {
	return &syncPlan{pins: pins, opts: opts, cached: make(map[domain.Hash]struct{})}
}

// write plans the materialization of f over base, the version last synced
// (tracked false when the path is new to the stored tree).
func (p *syncPlan) write(f materializedFile, base clientDomain.SnapshotFile, tracked bool) {
	if p.opts.keepLocalChanges {
		p.replaced = append(p.replaced, overwriteCandidate{target: f, base: base, tracked: tracked})
	}
	job := materializeJob{file: f, prevMode: base.Mode}
	if directEligible(f, p.pins) {
		p.directJobs = append(p.directJobs, job)
	} else {
		p.jobs = append(p.jobs, job)
		for _, h := range f.ChunkHashes {
			p.cached[h] = struct{}{}
		}
	}
	p.jobFiles = append(p.jobFiles, f)
	p.want = append(p.want, f.ChunkHashes...)
}

// run carries the plan out: the guards first (link targets, local changes),
// then the downloads, the writes, the removals and the fingerprints.
func (p *syncPlan) run(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, root string, scope clientDomain.ChunkScope, progress ...DownloadProgress) error {
	jobs, directJobs, jobFiles, cached, removals := p.jobs, p.directJobs, p.jobFiles, p.cached, p.removals
	if err := checkSyncTargets(root, jobFiles, removals); err != nil {
		return err
	}

	// What stands where a file becomes a folder or a folder a file is
	// checked before anything changes, forced or not.
	swaps, err := planSwaps(ctx, root, lr, jobFiles, removals)
	if err != nil {
		return err
	}
	conflicts, err := localChangeConflicts(ctx, root, lr, p.replaced)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return localChangesError(conflicts)
	}
	if len(jobFiles) > 0 || len(removals) > 0 {
		if err := clearSyncedTree(lr); err != nil {
			return err
		}
	}
	// A tracked file where a written file needs a folder goes before the
	// writes are prepared: their temp files are created in that folder. The
	// stored tree still names it, so an interrupted sync recovers on retry.
	if err := swaps.removeBlocking(ctx, root); err != nil {
		return err
	}
	removals = swaps.left(removals)

	missing, err := lr.MissingChunks(p.want)
	if err != nil {
		return err
	}
	// Release the hash list before the downloads; nothing reads it after this.
	p.want = nil

	direct, err := newDirectPlan(root, directJobs)
	if err != nil {
		return err
	}
	defer direct.cleanup()
	missingSet := make(map[domain.Hash]struct{}, len(missing))
	for _, h := range missing {
		missingSet[h] = struct{}{}
	}
	local := make([]domain.Hash, 0, len(direct.targets))
	for h := range direct.targets {
		if _, gone := missingSet[h]; !gone {
			local = append(local, h)
		}
	}
	if err := direct.copyLocal(ctx, local, lr.LoadChunk); err != nil {
		return err
	}
	// The estimate consumes the set, which nothing needs after this point.
	estimated := estimateMissingBytes(jobFiles, missingSet)

	sink := func(c *domain.ChunkData) error {
		if _, ok := cached[c.Hash]; ok {
			if err := lr.StoreChunks([]*domain.ChunkData{c}); err != nil {
				return err
			}
		}
		if direct.wants(c.Hash) {
			return direct.write(c.Hash, c.Data)
		}
		return nil
	}
	if err := downloadChunksStream(ctx, client, scope, missing, estimated, sink, syncStreamTarget(lr, direct, cached), progress...); err != nil {
		return err
	}
	// Every file is downloaded; a folder a file takes the place of goes now,
	// right before the files are swapped in.
	if err := swaps.clearFolders(ctx, root); err != nil {
		return err
	}
	statEntries, err := materializeAll(ctx, root, jobs, lr.OpenChunk)
	if err != nil {
		return err
	}
	if err := direct.finish(ctx); err != nil {
		return err
	}
	for path, entry := range direct.entries {
		statEntries[path] = entry
	}

	if err := removeLeftFiles(ctx, root, lr, removals); err != nil {
		return err
	}

	if len(statEntries) > 0 {
		if err := lr.SaveStatEntries(statEntries); err != nil {
			return err
		}
	}
	return nil
}

// checkSyncTargets refuses a sync, before anything is read, downloaded or
// written, when a file it writes or removes sits below a symbolic link or
// junction.
func checkSyncTargets(root string, writes []materializedFile, removals []clientDomain.SnapshotFile) error {
	paths := make([]string, 0, len(writes))
	for _, f := range writes {
		paths = append(paths, f.Path)
	}
	if err := checkWriteLayout(paths); err != nil {
		return err
	}
	if err := checkTargets(root, "write", paths); err != nil {
		return err
	}
	paths = paths[:0]
	for _, f := range removals {
		paths = append(paths, f.Path)
	}
	return checkTargets(root, "remove", paths)
}

// checkWriteLayout refuses a sync that writes a path both as a file and as a
// folder (a file and another file below it). No working copy can hold that;
// a server with the tree check never stores it, so it means a commit stored
// before the check. Found here, the sync stops before anything is written
// instead of failing halfway on "file exists".
func checkWriteLayout(paths []string) error {
	if len(paths) < 2 {
		return nil
	}
	files := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		files[p] = struct{}{}
	}
	checked := map[string]struct{}{}
	var clashes []string
	for _, p := range paths {
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			dir := p[:i]
			if _, done := checked[dir]; done {
				break
			}
			checked[dir] = struct{}{}
			if _, isFile := files[dir]; isFile {
				clashes = append(clashes, dir)
			}
		}
	}
	if len(clashes) == 0 {
		return nil
	}
	sort.Strings(clashes)
	shown := clashes
	more := ""
	if len(shown) > stagedClashLimit {
		shown = shown[:stagedClashLimit]
		more = fmt.Sprintf(" (and %d more)", len(clashes)-stagedClashLimit)
	}
	quoted := make([]string, 0, len(shown))
	for _, p := range shown {
		quoted = append(quoted, strconv.Quote(p))
	}
	return clientDomain.NewUserError(fmt.Sprintf("cannot check out this commit: it holds %s%s as both a file and a folder, which no working copy can hold. Nothing was written. The commit is damaged; a commit that deletes the file or the files below it repairs the branch", strings.Join(quoted, ", "), more)).
		WithHint("check out an earlier commit, or push the deletion from a clone that still has one", "nipa log")
}

type chunkStreamStore interface {
	StoreChunkFrom(hash domain.Hash, r io.Reader) (int64, error)
}

// syncStreamTarget decides where a sync's downloaded chunks are streamed, so
// none of them is buffered whole: a chunk that only goes into working files
// straight to its offsets there, a chunk the object cache keeps (text, pinned
// content) into the cache, and a chunk needed by both into the cache first
// and from there into the working files. A repository that cannot store from
// a stream keeps its cached chunks on the buffered path.
func syncStreamTarget(lr any, direct *directPlan, cached map[domain.Hash]struct{}) *chunkStreamTarget {
	store, canStore := lr.(chunkStreamStore)
	loader, canLoad := lr.(interface {
		LoadChunk(hash domain.Hash) ([]byte, error)
	})
	canStore = canStore && canLoad
	return &chunkStreamTarget{
		wants: func(h domain.Hash) bool {
			if _, keep := cached[h]; keep {
				return canStore
			}
			return direct.wants(h)
		},
		receive: func(h domain.Hash, body io.Reader) (int64, error) {
			if _, keep := cached[h]; !keep {
				return direct.receive(h, body)
			}
			n, err := store.StoreChunkFrom(h, body)
			if err != nil || !direct.wants(h) {
				return n, err
			}
			data, err := loader.LoadChunk(h)
			if err != nil {
				return n, err
			}
			return n, direct.place(h, data)
		},
		settle: func(h domain.Hash) error {
			if direct.wants(h) {
				return direct.settle(h)
			}
			return nil
		},
	}
}

// removeLeftFiles deletes the working files of paths that left the tree,
// keeping any that hold local changes. A fresh stat fingerprint decides
// without reading the file; the rest are hashed on a worker pool.
func removeLeftFiles(ctx context.Context, root string, lr any, removals []clientDomain.SnapshotFile) error {
	if len(removals) == 0 {
		return nil
	}
	paths := make([]string, 0, len(removals))
	for _, f := range removals {
		paths = append(paths, f.Path)
	}
	statCache, err := statEntriesFor(lr, paths)
	if err != nil {
		return err
	}
	var (
		mu   sync.Mutex
		gone []string
	)
	err = runPool(ctx, removals, func(base clientDomain.SnapshotFile) error {
		removed, err := guardedRemoveCached(root, base, statCache)
		if err != nil {
			return fmt.Errorf("remove %s: %w", base.Path, err)
		}
		if removed {
			mu.Lock()
			gone = append(gone, base.Path)
			mu.Unlock()
		}
		return nil
	})
	// The folders the removals emptied go too, as a fresh clone has none.
	pruneEmptiedFolders(root, gone)
	return err
}

// statEntriesFor loads the fingerprints of paths: single rows for a few
// paths when the repository can, the whole cache otherwise, none when the
// repository keeps no cache.
func statEntriesFor(lr any, paths []string) (map[string]clientDomain.StatEntry, error) {
	if getter, ok := lr.(statEntryGetter); ok && len(paths) <= maxSingleRowStatReads {
		return getter.StatEntries(paths)
	}
	if loader, ok := lr.(statCacheLoader); ok {
		return loader.LoadStatCache()
	}
	return nil, nil
}

const maxChunkStoreWorkers = 8

type chunkStoreLocalRepo interface {
	StoreChunks(chunks []*domain.ChunkData) error
}

// downloadMissing fetches the missing chunks into the local object store.
func downloadMissing(ctx context.Context, client chunkDownloader, lr chunkStoreLocalRepo, scope clientDomain.ChunkScope, missing []domain.Hash, estimatedBytes int64, progress ...DownloadProgress) error {
	store := func(c *domain.ChunkData) error { return lr.StoreChunks([]*domain.ChunkData{c}) }
	var stream *chunkStreamTarget
	if streamStore, ok := lr.(chunkStreamStore); ok {
		stream = &chunkStreamTarget{
			wants:   func(domain.Hash) bool { return true },
			receive: streamStore.StoreChunkFrom,
			settle:  func(domain.Hash) error { return nil },
		}
	}
	return downloadChunksStream(ctx, client, scope, missing, estimatedBytes, store, stream, progress...)
}

// chunkStreamTarget takes chunks as a stream instead of a buffered slice:
// receive writes and verifies one chunk and may repeat, settle books it once.
type chunkStreamTarget struct {
	wants   func(domain.Hash) bool
	receive func(domain.Hash, io.Reader) (int64, error)
	settle  func(domain.Hash) error
}

type streamingDownloader interface {
	DownloadChunksStream(ctx context.Context, scope clientDomain.ChunkScope, hashes []domain.Hash, stream *clientDomain.ChunkStream, onChunk func(h domain.Hash, data []byte) error) error
}

// downloadChunksStream fetches the missing chunks. The transport delivers
// chunks one at a time, so delivery only books progress and hands each chunk to
// a worker pool that verifies its hash and passes it to sink. It takes an
// optional stream target: when
// the client can stream, the chunks the target wants go from the response body
// straight to it and are never held in memory; with a client that cannot,
// every chunk reaches sink.
func downloadChunksStream(ctx context.Context, client chunkDownloader, scope clientDomain.ChunkScope, missing []domain.Hash, estimatedBytes int64, sink func(*domain.ChunkData) error, stream *chunkStreamTarget, progress ...DownloadProgress) error {
	if len(missing) == 0 {
		return nil
	}
	var prog DownloadProgress
	if len(progress) > 0 {
		prog = progress[0]
	}
	if prog != nil {
		prog.DownloadStart(len(missing), estimatedBytes)
	}

	workers := max(1, min(runtime.GOMAXPROCS(0), maxChunkStoreWorkers))
	store := make(chan *domain.ChunkData, workers)
	var (
		wg       sync.WaitGroup
		once     sync.Once
		storeErr atomic.Pointer[error]
	)
	fail := func(err error) { once.Do(func() { storeErr.Store(&err) }) }
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range store {
				if storeErr.Load() != nil {
					continue
				}
				if chunker.Sum(c.Data) != c.Hash {
					fail(fmt.Errorf("chunk hash mismatch for %s", c.Hash))
					continue
				}
				if err := sink(c); err != nil {
					fail(err)
				}
			}
		}()
	}

	// Streamed chunks are booked from the transfer workers, buffered ones from
	// the transport's delivery callback, so the books need a lock.
	var (
		mu          sync.Mutex
		doneObjects int
		doneBytes   int64
	)
	seen := make(map[domain.Hash]bool, len(missing))
	book := func(h domain.Hash, size int64) bool {
		mu.Lock()
		defer mu.Unlock()
		if seen[h] {
			return false
		}
		seen[h] = true
		if prog != nil {
			doneObjects++
			doneBytes += size
			prog.DownloadProgress(doneObjects, doneBytes)
		}
		return true
	}
	onChunk := func(h domain.Hash, data []byte) error {
		if failed := storeErr.Load(); failed != nil {
			return *failed
		}
		if book(h, int64(len(data))) {
			store <- &domain.ChunkData{Hash: h, Data: data}
		}
		return nil
	}

	var err error
	if streamer, ok := client.(streamingDownloader); ok && stream != nil {
		err = streamer.DownloadChunksStream(ctx, scope, missing, &clientDomain.ChunkStream{
			Wants: stream.wants,
			Write: func(h domain.Hash, body io.Reader) error {
				if failed := storeErr.Load(); failed != nil {
					return *failed
				}
				n, err := stream.receive(h, body)
				if err != nil {
					return err
				}
				if !book(h, n) {
					return nil
				}
				return stream.settle(h)
			},
		}, onChunk)
	} else {
		err = client.DownloadChunks(ctx, scope, missing, onChunk)
	}
	close(store)
	wg.Wait()
	if failed := storeErr.Load(); failed != nil {
		return *failed
	}
	if err != nil {
		return err
	}
	for _, h := range missing {
		if !seen[h] {
			return fmt.Errorf("server did not return chunk %s", h)
		}
	}
	if prog != nil {
		prog.DownloadEnd()
	}
	return nil
}

type materializedFile struct {
	Path        string
	Mode        int
	SizeBytes   int64
	Encoding    string
	FileHash    domain.Hash
	ChunkHashes []domain.Hash
	ChunkSizes  []int64
}

// walkTreeFiles calls fn for every file of the tree with its repository path.
func walkTreeFiles(node *domain.TreeNode, prefix string, fn func(path string, file *domain.File)) {
	if node == nil {
		return
	}
	for _, f := range node.FileChildren {
		fn(strings.TrimPrefix(prefix+"/"+f.Name, "/"), f)
	}
	for _, child := range node.TreeChildren {
		walkTreeFiles(child, prefix+"/"+child.Name, fn)
	}
}

// treeFileHash is the file hash of a tree file, computed from its chunk
// hashes without copying them.
func treeFileHash(f *domain.File) domain.Hash {
	hasher := chunker.NewHasher()
	for i := range f.Chunks {
		_, _ = hasher.Write(f.Chunks[i].Hash[:])
	}
	return hasher.Sum()
}

func materializedFromTree(path string, f *domain.File) materializedFile {
	hashes := make([]domain.Hash, 0, len(f.Chunks))
	sizes := make([]int64, 0, len(f.Chunks))
	for _, c := range f.Chunks {
		hashes = append(hashes, c.Hash)
		sizes = append(sizes, c.SizeBytes)
	}
	return materializedFile{
		Path:        path,
		Mode:        f.Mode,
		SizeBytes:   f.SizeBytes,
		Encoding:    f.Encoding,
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}
}

func flattenTree(node *domain.TreeNode, prefix string) []materializedFile {
	var files []materializedFile
	walkTreeFiles(node, prefix, func(path string, f *domain.File) {
		files = append(files, materializedFromTree(path, f))
	})
	return files
}

func estimateBytesToDownload(files []materializedFile, missing []domain.Hash) int64 {
	missingSet := make(map[domain.Hash]struct{}, len(missing))
	for _, h := range missing {
		missingSet[h] = struct{}{}
	}
	return estimateMissingBytes(files, missingSet)
}

// estimateMissingBytes is estimateBytesToDownload over a set it may consume:
// each chunk is counted once and removed.
func estimateMissingBytes(files []materializedFile, missingSet map[domain.Hash]struct{}) int64 {
	var total int64
	for _, f := range files {
		matched, known := 0, int64(0)
		unknown := false
		for i, h := range f.ChunkHashes {
			if _, ok := missingSet[h]; !ok {
				continue
			}
			delete(missingSet, h)
			matched++
			if i < len(f.ChunkSizes) && f.ChunkSizes[i] > 0 {
				known += f.ChunkSizes[i]
			} else {
				unknown = true
			}
		}
		if matched == 0 {
			continue
		}
		if unknown {
			// The tree manifest does not carry per-chunk sizes, so scale the
			// file size by the missing chunk ratio (exact for a full download).
			total += f.SizeBytes * int64(matched) / int64(len(f.ChunkHashes))
		} else {
			total += known
		}
	}
	return total
}

func fileExists(root, path string) bool {
	fp := filepath.Join(root, filepath.FromSlash(path))
	_, err := os.Stat(fp)
	return err == nil
}

func isReadOnlyIn(fsys replaceFS, p string) bool {
	info, err := fsys.Lstat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o200 == 0
}

func materializedMode(mode int) os.FileMode {
	switch mode {
	case 3: // executable
		return 0o755
	case 1: // read-only
		return 0o444
	default: // read-write (and unspecified)
		return 0o644
	}
}

func guardedRemove(root string, base clientDomain.SnapshotFile) (bool, error) {
	return guardedRemoveCached(root, base, nil)
}

// guardedRemoveCached is guardedRemove that trusts a fresh stat fingerprint
// instead of reading the file.
func guardedRemoveCached(root string, base clientDomain.SnapshotFile, statCache map[string]clientDomain.StatEntry) (bool, error) {
	if err := checkRemoveInsideRoot(root, base.Path); err != nil {
		return false, err
	}
	wt := acquireWorkTree(root)
	defer wt.release()
	name := filepath.FromSlash(base.Path)
	dir, done, err := wt.dirOf("remove", name, false)
	if os.IsNotExist(err) || isNotDir(err) {
		// No folder, or a file where its folder was: the path is gone.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer done()
	leaf := filepath.Base(name)
	info, err := dir.Lstat(leaf)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err == nil && info.IsDir() {
		// A folder took the file's place (or the path became a directory
		// already): not the tracked content, so it is kept.
		return false, nil
	}
	if entry, ok := statCache[base.Path]; ok {
		if err == nil && info.Mode().IsRegular() && statMatches(entry, info) {
			if entry.Hash != base.Hash {
				return false, nil
			}
			err := dir.Remove(leaf)
			if os.IsNotExist(err) {
				err = nil
			}
			return true, err
		}
	}
	f, err := dir.Open(leaf)
	if isRootEscape(err) {
		// The file was replaced by a link out of the clone: not content
		// this sync wrote, so it is kept like any local change.
		return false, nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return false, err
	}
	var hashes []domain.Hash
	if _, err := chunker.Encode(f, base.Path, isBinary, storedEncoding(base.Encoding), func(c chunker.EncodedChunk) error {
		hashes = append(hashes, c.Hash)
		return nil
	}); err != nil {
		return false, err
	}
	if chunker.FileHash(hashes) == base.Hash {
		return true, dir.Remove(leaf)
	}
	return false, nil
}
