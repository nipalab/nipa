package usecase

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/treedelta"
)

type pushClient interface {
	Connect(ctx context.Context, host string) error
	Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash, baseCommitID string) (*serverDomain.PushResult, error)
	UploadChunks(ctx context.Context, scope domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error)
}

type pushLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	ClearStaged() error
	SaveSyncedTree(root *serverDomain.TreeNode, commitID string, sparse []string) error
	ApplyPush(in domain.PushApply) error
	SaveCommit(commitID, commitHash string) error
	StoreChunks(chunks []*serverDomain.ChunkData) error
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	LoadMergeState() (*domain.MergeState, error)
	ClearMergeState() error
	LoadRevertState() (*domain.RevertState, error)
	ClearRevertState() error
	LoadCommit() (*domain.LocalCommit, error)
	SaveStatEntries(entries map[string]domain.StatEntry) error
	ListPins() ([]string, error)
	UnpinPaths(paths []string) error
}

type Push struct {
	auth       *Auth
	pushClient pushClient
	localRepo  pushLocalRepo
}

func NewPush(auth *Auth, pushClient pushClient, localRepo pushLocalRepo) *Push {
	return &Push{
		auth:       auth,
		pushClient: pushClient,
		localRepo:  localRepo,
	}
}

func (p *Push) Run(ctx context.Context, root, message string, progress ...UploadProgress) (err error) {
	ctx, span := obs.StartSpan(ctx, "nipa.push")
	defer func() { obs.FinishSpan(span, err) }()
	// What the push can leave unreferenced in the object cache decides how
	// much the prune after it has to look at (pruneAfterPush).
	var effect *pushCacheEffect
	defer func() { pruneAfterPush(p.localRepo, effect, err) }()

	if strings.TrimSpace(message) == "" {
		return domain.NewUserError("commit message is required")
	}
	if err := p.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(p.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := p.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	if cfg.Head != nil {
		return domain.NewUserError(fmt.Sprintf("HEAD is detached at tag %q; push happens on a branch", cfg.Head.Name)).
			WithHint("switch to a branch first", "nipa switch <branch>")
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	span.SetAttributes(
		attribute.String(obs.AttrOrg, nipaUrl.Org),
		attribute.String(obs.AttrProject, nipaUrl.Project),
		attribute.String(obs.AttrBranch, cfg.Branch),
	)
	if err := p.pushClient.Connect(ctx, nipaUrl.Origin); err != nil {
		return err
	}
	if err := p.auth.MakeSureLoggedIn(ctx, nipaUrl.Host); err != nil {
		return err
	}

	if err := refuseShelfPending(p.localRepo); err != nil {
		return err
	}
	mergeState, err := p.localRepo.LoadMergeState()
	if err != nil {
		return err
	}
	revertState, err := p.localRepo.LoadRevertState()
	if err != nil {
		return err
	}
	if revertState != nil && len(revertState.Targets) > 1 {
		return domain.NewUserError("a revert sequence is in progress; run nipa revert --continue").
			WithHint("the working copy is mid revert-sequence", "nipa revert --continue")
	}

	var baseTreeHash, baseCommitID, parent2 string
	switch {
	case mergeState != nil:
		parent2 = mergeState.SourceCommitHash
		baseTreeHash = mergeState.TargetTreeHash
		baseCommitID = mergeState.TargetCommitID
	case revertState != nil:
		baseTreeHash = revertState.CurrentTreeHash
		baseCommitID = revertState.CurrentCommitID
	default:
		if pin, err := p.localRepo.LoadCommit(); err == nil && pin != nil {
			baseCommitID = pin.CommitID
		}
	}

	_, landed, err := p.pushStagedEffect(ctx, root, nipaUrl, cfg.Branch, message, baseTreeHash, baseCommitID, parent2, progress...)
	if err != nil {
		return err
	}
	// A merge or revert cached content of its own while it was pending, which
	// only the full sweep finds.
	if mergeState == nil && revertState == nil {
		effect = landed
	}
	if err := p.localRepo.ClearMergeState(); err != nil {
		return err
	}
	return p.localRepo.ClearRevertState()
}

// Plan reports what the next push would upload and land without connecting to
// the server or mutating local state. The upload estimate counts chunks that
// are not in the local object cache, so it is an upper bound.
func (p *Push) Plan(ctx context.Context, root string) (*domain.Plan, error) {
	if err := p.localRepo.Init(root); err != nil {
		return nil, err
	}
	unlock, err := lockShared(p.localRepo)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cfg, err := p.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Head != nil {
		return nil, domain.NewUserError(fmt.Sprintf("HEAD is detached at tag %q; push happens on a branch", cfg.Head.Name)).
			WithHint("switch to a branch first", "nipa switch <branch>")
	}
	if _, err := domain.ParseNipaUrl(cfg.Url); err != nil {
		return nil, err
	}
	revertState, err := p.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if revertState != nil && len(revertState.Targets) > 1 {
		return nil, domain.NewUserError("a revert sequence is in progress; run nipa revert --continue").
			WithHint("the working copy is mid revert-sequence", "nipa revert --continue")
	}
	staged, baseByPath, _, err := p.stagedBase()
	if err != nil {
		return nil, err
	}
	toRead, removed, err := classifyStaged(root, staged, baseByPath)
	if err != nil {
		return nil, err
	}
	if err := p.checkStagedTree(staged, baseByPath, toRead, removed); err != nil {
		return nil, err
	}
	collector := newPlanCollector()
	changes := make([]domain.PlanChange, 0, len(toRead)+len(removed))
	for _, sf := range toRead {
		file, err := scanPushFile(sf, collector)
		if err != nil {
			return nil, err
		}
		base, ok := baseByPath[sf.path]
		switch {
		case !ok:
			changes = append(changes, domain.PlanChange{Path: sf.path, Status: "A", Binary: file.IsBinary, SizeBytes: file.SizeBytes})
		case base.Hash != file.FileHash || normalizedMode(base.Mode) != trackedModeFromPerm(sf.info.Mode(), base.Mode):
			changes = append(changes, domain.PlanChange{Path: sf.path, Status: "M", Binary: file.IsBinary, SizeBytes: file.SizeBytes})
		}
	}
	for _, path := range removed {
		changes = append(changes, domain.PlanChange{Path: path, Status: "D", SizeBytes: baseByPath[path].SizeBytes})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })

	missing, err := p.localRepo.MissingChunks(collector.order)
	if err != nil {
		return nil, err
	}
	plan := &domain.Plan{Kind: "push", Changes: changes, UploadObjects: len(missing)}
	for _, h := range missing {
		plan.UploadBytes += collector.sizes[h]
	}
	return plan, nil
}

// stagedBase returns the staged paths with the tracked rows they replace and
// the snapshot's tree hash. A repository that reads by path is asked for the
// staged paths only, so the cost follows the push, not the repository.
func (p *Push) stagedBase() ([]string, map[string]domain.SnapshotFile, string, error) {
	staged, stagedErr := p.localRepo.ListStaged()
	if stagedErr != nil {
		staged = nil
	}
	var (
		baseByPath map[string]domain.SnapshotFile
		treeHash   string
		err        error
	)
	if scoped, ok := p.localRepo.(scopedSnapshotRepo); ok {
		if baseByPath, err = scoped.SnapshotFiles(staged); err == nil {
			treeHash, err = scoped.SnapshotTreeHash()
		}
	} else {
		var snapshot *domain.Snapshot
		if snapshot, err = p.localRepo.Snapshot(); err == nil {
			treeHash = snapshot.TreeHash
			baseByPath = make(map[string]domain.SnapshotFile, len(snapshot.Files))
			for _, f := range snapshot.Files {
				baseByPath[f.Path] = f
			}
		}
	}
	switch {
	case err != nil:
		return nil, nil, "", err
	case stagedErr != nil:
		return nil, nil, "", stagedErr
	case len(staged) == 0:
		return nil, nil, "", domain.NewUserError("nothing staged to push; run nipa add first")
	}
	return staged, baseByPath, treeHash, nil
}

func (p *Push) pushStaged(ctx context.Context, root string, nipaUrl *domain.NipaUrl, branch, message, baseTreeHash, baseCommitID, parent2 string, progress ...UploadProgress) (*serverDomain.PushResult, error) {
	result, _, err := p.pushStagedEffect(ctx, root, nipaUrl, branch, message, baseTreeHash, baseCommitID, parent2, progress...)
	return result, err
}

// pushStagedEffect is pushStaged that also reports what the push can have
// left unreferenced in the object cache.
func (p *Push) pushStagedEffect(ctx context.Context, root string, nipaUrl *domain.NipaUrl, branch, message, baseTreeHash, baseCommitID, parent2 string, progress ...UploadProgress) (*serverDomain.PushResult, *pushCacheEffect, error) {
	if err := p.localRepo.Init(root); err != nil {
		return nil, nil, err
	}
	staged, baseByPath, snapshotTreeHash, err := p.stagedBase()
	if err != nil {
		return nil, nil, err
	}

	toRead, removed, err := classifyStaged(root, staged, baseByPath)
	if err != nil {
		return nil, nil, err
	}
	// A path that would be both a file and a folder is refused by the server;
	// say so before the upload instead of after it.
	if err := p.checkStagedTree(staged, baseByPath, toRead, removed); err != nil {
		return nil, nil, err
	}
	pins, err := p.localRepo.ListPins()
	if err != nil {
		return nil, nil, err
	}
	for i := range toRead {
		toRead[i].cache = !toRead[i].isBinary || pinsCover(pins, toRead[i].path)
	}
	var estBytes int64
	var estObjects int
	for _, sf := range toRead {
		estBytes += sf.info.Size()
		estObjects += estimateObjects(sf.info.Size(), sf.cfg)
	}

	var prog UploadProgress
	if len(progress) > 0 {
		prog = progress[0]
	}
	doneObjects, doneBytes := 0, int64(0)
	var progressMu sync.Mutex
	var onChunk func(ch *serverDomain.ChunkData)
	if prog != nil {
		prog.UploadStart(estObjects, estBytes)
		if verify, ok := prog.(UploadVerifyProgress); ok {
			ctx = domain.WithVerifyObserver(ctx, func(objects int) {
				progressMu.Lock()
				defer progressMu.Unlock()
				verify.UploadVerify(objects)
			})
		}
		onChunk = func(ch *serverDomain.ChunkData) {
			progressMu.Lock()
			defer progressMu.Unlock()
			doneObjects++
			doneBytes += chunkRawSize(ch)
			prog.UploadProgress(doneObjects, doneBytes)
		}
	}

	uploadCtx, uploadSpan := obs.StartSpan(ctx, "push.upload",
		attribute.Int(obs.AttrPushFiles, len(toRead)),
		attribute.Int64("nipa.push.upload_bytes", estBytes))
	uploader := newChunkUploader(uploadCtx, p.pushClient, p.localRepo,
		domain.ChunkScope{Org: nipaUrl.Org, Project: nipaUrl.Project}, onChunk)
	files, statEntries, err := scanPushFiles(toRead, uploader)
	if err != nil {
		uploader.abort()
		obs.FinishSpan(uploadSpan, err)
		return nil, nil, err
	}
	if err := uploader.close(); err != nil {
		obs.FinishSpan(uploadSpan, err)
		return nil, nil, err
	}
	obs.FinishSpan(uploadSpan, nil)
	if prog != nil {
		prog.UploadEnd()
	}
	if len(statEntries) > 0 {
		if err := p.localRepo.SaveStatEntries(statEntries); err != nil {
			return nil, nil, err
		}
	}

	if baseTreeHash == "" {
		baseTreeHash = snapshotTreeHash
	}
	commitCtx, commitSpan := obs.StartSpan(ctx, "push.commit",
		attribute.String(obs.AttrBranch, branch),
		attribute.Int(obs.AttrPushFiles, len(files)),
		attribute.Int(obs.AttrPushRemoved, len(removed)))
	result, err := p.pushClient.Push(commitCtx, nipaUrl.Org, nipaUrl.Project, branch, baseTreeHash, message, files, removed, parent2, baseCommitID)
	if err != nil {
		obs.FinishSpan(commitSpan, err)
		return nil, nil, err
	}
	if err := p.localRepo.SaveCommit(result.CommitID.Base36(), result.CommitHash.String()); err != nil {
		obs.FinishSpan(commitSpan, err)
		return nil, nil, err
	}
	commitSpan.SetAttributes(
		attribute.String("nipa.push.commit", result.CommitID.Base36()),
		attribute.String("nipa.push.commit_hash", result.CommitHash.String()),
	)
	obs.FinishSpan(commitSpan, nil)

	applied, err := p.saveLandedTree(ctx, nipaUrl, branch, baseCommitID, result, files, removed)
	if err != nil {
		return nil, nil, err
	}
	if err := p.localRepo.ClearStaged(); err != nil {
		return nil, nil, err
	}
	if landed := landedPins(pins, files, removed); len(landed) > 0 {
		if err := p.localRepo.UnpinPaths(landed); err != nil {
			slog.Warn("unpin pushed paths failed", "error", err)
		}
	}
	return result, newPushCacheEffect(applied, baseByPath, files, removed), nil
}

// Values of the nipa.push.tree_sync span attribute: how the stored tree was
// brought to the pushed commit.
const (
	treeSyncLocal   = "local"
	treeSyncRefetch = "refetch"
)

// saveLandedTree makes the stored tree the tree of the commit the push just
// created. A full checkout applies the push to the stored rows
// (localrepo.ApplyPush), which checks the result against the root hash in the
// push response; a sparse checkout, a stored tree that is not the push base
// and a root hash that does not match all save the new head's manifest
// instead, as every push did before. The commit already landed, so a failed
// local apply never fails the push: the manifest fetch decides. It reports
// whether the push was applied locally, in which case only the rows of the
// pushed and removed paths changed.
func (p *Push) saveLandedTree(ctx context.Context, nipaUrl *domain.NipaUrl, branch, baseCommitID string, result *serverDomain.PushResult, files []*serverDomain.PushFile, removed []string) (applied bool, err error) {
	ctx, span := obs.StartSpan(ctx, "push.tree")
	defer func() { obs.FinishSpan(span, err) }()

	commitID := result.CommitID.Base36()
	var sparse []string
	reason := "config"
	cfg, cfgErr := p.localRepo.LoadConfig()
	if cfgErr == nil && cfg != nil {
		sparse = cfg.Sparse
		reason = "sparse"
		if len(sparse) == 0 {
			applyErr := p.localRepo.ApplyPush(domain.PushApply{
				Files:        files,
				Removed:      removed,
				BaseCommitID: baseCommitID,
				CommitID:     commitID,
				TreeHash:     result.TreeHash,
				View:         result.View,
			})
			if applyErr == nil {
				span.SetAttributes(attribute.String("nipa.push.tree_sync", treeSyncLocal))
				return true, nil
			}
			reason = applyFallbackReason(applyErr, result.TreeHash)
		}
	}
	span.SetAttributes(
		attribute.String("nipa.push.tree_sync", treeSyncRefetch),
		attribute.String("nipa.push.tree_sync_reason", reason),
	)
	// The manifest of the commit this push created, with the sparse set the
	// config holds. A client that cannot ask by commit reads the branch head
	// right after the push made that commit the head.
	rootTree, view, err := fetchManifest(ctx, p.pushClient, nipaUrl.Org, nipaUrl.Project, treedelta.Target{CommitID: commitID}, sparse, func() (*serverDomain.TreeNode, error) {
		return p.pushClient.GetTreeNodeManifest(ctx, nipaUrl.Org, nipaUrl.Project, branch, sparse)
	})
	if err != nil {
		return false, err
	}
	return false, saveSyncedTree(p.localRepo, rootTree, commitID, sparse, view)
}

// applyFallbackReason names why a local apply gave way to the manifest fetch
// and logs it; the user never sees it.
func applyFallbackReason(err error, expected serverDomain.Hash) string {
	var mismatch *domain.PushApplyMismatchError
	switch {
	case errors.As(err, &mismatch):
		slog.Debug("pushed tree hash differs from the locally applied one; fetching the manifest",
			"expected", mismatch.Expected.String(), "computed", mismatch.Computed.String())
		return "mismatch"
	case errors.Is(err, domain.ErrPushApplyBase):
		slog.Debug("stored tree is not the push base; fetching the manifest", "expected", expected.String())
		return "base"
	default:
		slog.Debug("applying the push locally failed; fetching the manifest", "expected", expected.String(), "error", err)
		return "error"
	}
}

// landedPins returns the exact-path pins a push landed; directory pins stay.
func landedPins(pins []string, files []*serverDomain.PushFile, removed []string) []string {
	if len(pins) == 0 {
		return nil
	}
	pinned := make(map[string]bool, len(pins))
	for _, pin := range pins {
		pinned[pin] = true
	}
	var out []string
	for _, path := range removed {
		if pinned[path] {
			out = append(out, path)
		}
	}
	for _, f := range files {
		if pinned[f.Path] {
			out = append(out, f.Path)
		}
	}
	return out
}

// uploadWindowBytes is the default chunk data buffered before a window is
// handed to the uploaders. The scanner keeps chunking while earlier windows
// transfer, so local hashing overlaps with network I/O. With 4 windows in
// flight and 2 queued, at most ~224 MiB is buffered; the client's transfer
// limiter still caps the PUTs across all windows, and one window's presign and
// confirm overlap the others' PUTs. A client may override the size
// (NIPA_UPLOAD_WINDOW_MIB / uploadWindowMiB) through uploadWindowSizer.
var uploadWindowBytes = 32 << 20

const (
	uploadWindowWorkers = 4
	uploadWindowQueue   = 2
)

// uploadWindowSizer is implemented by upload clients configured with their
// own window size; 0 keeps the default.
type uploadWindowSizer interface {
	UploadWindowBytes() int
}

// windowBytesFor returns the upload window size for client.
func windowBytesFor(client ChunkUploadClient) int {
	if sizer, ok := client.(uploadWindowSizer); ok {
		if size := sizer.UploadWindowBytes(); size > 0 {
			return size
		}
	}
	return uploadWindowBytes
}

type chunkUploader struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    ChunkUploadClient
	localRepo pushLocalRepo
	scope     domain.ChunkScope
	onChunk   func(ch *serverDomain.ChunkData)

	seen        map[serverDomain.Hash]bool
	batch       []*serverDomain.ChunkData
	bytes       int
	windowBytes int

	batchMu sync.Mutex

	cacheMu sync.Mutex
	cache   map[serverDomain.Hash]bool

	windows chan []*serverDomain.ChunkData
	wg      sync.WaitGroup

	mu  sync.Mutex
	err error
}

func newChunkUploader(ctx context.Context, client ChunkUploadClient, localRepo pushLocalRepo, scope domain.ChunkScope, onChunk func(*serverDomain.ChunkData)) *chunkUploader {
	uploadCtx, cancel := context.WithCancel(ctx)
	u := &chunkUploader{
		ctx:         uploadCtx,
		cancel:      cancel,
		client:      client,
		localRepo:   localRepo,
		scope:       scope,
		onChunk:     onChunk,
		windowBytes: windowBytesFor(client),
		seen:        make(map[serverDomain.Hash]bool),
		cache:       make(map[serverDomain.Hash]bool),
		windows:     make(chan []*serverDomain.ChunkData, uploadWindowQueue),
	}
	for i := 0; i < uploadWindowWorkers; i++ {
		u.wg.Add(1)
		go u.run()
	}
	return u
}

func (u *chunkUploader) run() {
	defer u.wg.Done()
	for window := range u.windows {
		if err := u.upload(window); err != nil {
			u.fail(err)
			return
		}
	}
}

func (u *chunkUploader) upload(window []*serverDomain.ChunkData) error {
	if u.localRepo != nil {
		if cached := u.cached(window); len(cached) > 0 {
			if err := u.localRepo.StoreChunks(cached); err != nil {
				return err
			}
		}
	}
	_, _, err := u.client.UploadChunks(u.ctx, u.scope, window, u.onChunk)
	return err
}

func (u *chunkUploader) cached(window []*serverDomain.ChunkData) []*serverDomain.ChunkData {
	u.cacheMu.Lock()
	defer u.cacheMu.Unlock()
	out := make([]*serverDomain.ChunkData, 0, len(window))
	for _, ch := range window {
		if u.cache[ch.Hash] {
			out = append(out, ch)
		}
	}
	return out
}

func (u *chunkUploader) add(ch *serverDomain.ChunkData, cache bool) error {
	u.batchMu.Lock()
	defer u.batchMu.Unlock()
	if u.ctx.Err() != nil {
		return u.failure()
	}
	if cache {
		u.cacheMu.Lock()
		u.cache[ch.Hash] = true
		u.cacheMu.Unlock()
	}
	if u.seen[ch.Hash] {
		return nil
	}
	u.seen[ch.Hash] = true
	u.batch = append(u.batch, ch)
	u.bytes += len(ch.Data)
	if u.bytes < u.windowBytes {
		return nil
	}
	return u.flushLocked()
}

func (u *chunkUploader) flush() error {
	u.batchMu.Lock()
	defer u.batchMu.Unlock()
	return u.flushLocked()
}

func (u *chunkUploader) flushLocked() error {
	if len(u.batch) == 0 {
		return nil
	}
	window := u.batch
	u.batch = nil
	u.bytes = 0
	select {
	case u.windows <- window:
		return nil
	case <-u.ctx.Done():
		return u.failure()
	}
}

func (u *chunkUploader) close() error {
	err := u.flush()
	close(u.windows)
	u.wg.Wait()
	failure := u.failure()
	u.cancel()
	if err != nil {
		return err
	}
	return failure
}

func (u *chunkUploader) abort() {
	u.cancel()
	close(u.windows)
	u.wg.Wait()
}

func (u *chunkUploader) fail(err error) {
	u.mu.Lock()
	if u.err == nil {
		u.err = err
	}
	u.mu.Unlock()
	u.cancel()
}

func (u *chunkUploader) failure() error {
	u.mu.Lock()
	err := u.err
	u.mu.Unlock()
	if err != nil {
		return err
	}
	return u.ctx.Err()
}

type stagedFile struct {
	path     string
	info     fs.FileInfo
	abs      string
	cfg      chunker.Config
	isBinary bool
	baseMode int
	cache    bool
}

// classifyStaged splits the staged paths into files to read and removals,
// checking existence and the .nipa guard for each.
func classifyStaged(root string, staged []string, baseByPath map[string]domain.SnapshotFile) ([]stagedFile, []string, error) {
	var toRead []stagedFile
	var removed []string
	for _, path := range staged {
		if isNipaPath(path) {
			return nil, nil, domain.NewUserError(fmt.Sprintf("cannot push path inside %q: %s", nipaDir, path))
		}
		abs := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Stat(abs)
		if _, inBase := baseByPath[path]; err == nil && inBase && info.IsDir() {
			// The tracked file became a folder: its row goes.
			err = fs.ErrNotExist
		}
		switch {
		case err == nil:
			if !info.Mode().IsRegular() {
				return nil, nil, domain.NewUserError(fmt.Sprintf("%q is not a regular file", path))
			}
			cfg, isBinary := profileForFile(path, abs)
			sf := stagedFile{path: path, info: info, abs: abs, cfg: cfg, isBinary: isBinary}
			if base, inBase := baseByPath[path]; inBase {
				sf.baseMode = normalizedMode(base.Mode)
			}
			toRead = append(toRead, sf)
		case isMissing(err):
			if _, inBase := baseByPath[path]; !inBase {
				return nil, nil, domain.NewUserError(fmt.Sprintf("staged file %q does not exist", path))
			}
			removed = append(removed, path)
		default:
			return nil, nil, err
		}
	}
	return toRead, removed, nil
}

func profileForFile(path, abs string) (chunker.Config, bool) {
	f, err := os.Open(abs)
	if err != nil {
		return chunker.DefaultConfig, false
	}
	defer func() { _ = f.Close() }()
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return chunker.DefaultConfig, false
	}
	return chunker.ConfigForFile(path, isBinary), isBinary
}

func chunkRawSize(ch *serverDomain.ChunkData) int64 {
	if ch.RawSize > 0 {
		return ch.RawSize
	}
	return int64(len(ch.Data))
}

func estimateObjects(size int64, cfg chunker.Config) int {
	if size <= 0 {
		return 0
	}
	n := int(size / cfg.Avg)
	if n < 1 {
		n = 1
	}
	return n
}

type chunkSink interface {
	add(ch *serverDomain.ChunkData, cache bool) error
}

// planCollector records chunk hashes and encoded sizes without storing or
// uploading anything.
type planCollector struct {
	sizes map[serverDomain.Hash]int64
	order []serverDomain.Hash
}

func newPlanCollector() *planCollector {
	return &planCollector{sizes: make(map[serverDomain.Hash]int64)}
}

func (c *planCollector) add(ch *serverDomain.ChunkData, _ bool) error {
	if _, ok := c.sizes[ch.Hash]; ok {
		return nil
	}
	c.sizes[ch.Hash] = int64(len(ch.Data))
	c.order = append(c.order, ch.Hash)
	return nil
}

func scanPushFile(sf stagedFile, sink chunkSink) (*serverDomain.PushFile, error) {
	f, err := os.Open(sf.abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var hashes []serverDomain.Hash
	var rawSize int64
	encoding, err := chunker.Encode(f, sf.path, sf.isBinary, "", func(c chunker.EncodedChunk) error {
		hashes = append(hashes, c.Hash)
		rawSize += c.RawSize
		return sink.add(&serverDomain.ChunkData{Hash: c.Hash, Data: c.Data, RawSize: c.RawSize}, sf.cache)
	})
	if err != nil {
		return nil, err
	}
	if rawSize != sf.info.Size() {
		return nil, domain.NewUserError(fmt.Sprintf("file %q changed while reading", sf.path)).
			WithHint("re-run nipa add after the write finishes", "nipa add "+sf.path)
	}
	return &serverDomain.PushFile{
		Path:        sf.path,
		Mode:        int(pushPerm(sf.info.Mode().Perm(), sf.baseMode)),
		SizeBytes:   rawSize,
		IsBinary:    sf.isBinary,
		Encoding:    encoding,
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}, nil
}

const maxPushScanWorkers = 8

func pushScanWorkers(n int) int {
	workers := runtime.GOMAXPROCS(0)
	if workers > maxPushScanWorkers {
		workers = maxPushScanWorkers
	}
	if workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

// scanPushFiles hashes files with bounded parallelism, overlapping disk reads
// across cores. Results are indexed so the push payload keeps path order; the
// first error stops the remaining work.
func scanPushFiles(staged []stagedFile, sink chunkSink) ([]*serverDomain.PushFile, map[string]domain.StatEntry, error) {
	if len(staged) == 0 {
		return nil, map[string]domain.StatEntry{}, nil
	}
	files := make([]*serverDomain.PushFile, len(staged))
	errs := make([]error, len(staged))
	var stopped atomic.Bool
	indices := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < pushScanWorkers(len(staged)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range indices {
				if stopped.Load() {
					continue
				}
				files[idx], errs[idx] = scanPushFile(staged[idx], sink)
				if errs[idx] != nil {
					stopped.Store(true)
				}
			}
		}()
	}
	for i := range staged {
		indices <- i
	}
	close(indices)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, nil, err
		}
	}
	statEntries := make(map[string]domain.StatEntry, len(staged))
	for i, file := range files {
		statEntries[staged[i].path] = statEntryFromInfo(staged[i].info, file.FileHash)
	}
	return files, statEntries, nil
}
