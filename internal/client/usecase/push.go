package usecase

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
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
	SaveTree(root *serverDomain.TreeNode) error
	SaveCommit(commitID, commitHash string) error
	StoreChunks(chunks []*serverDomain.ChunkData) error
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	LoadMergeState() (*domain.MergeState, error)
	ClearMergeState() error
	LoadRevertState() (*domain.RevertState, error)
	ClearRevertState() error
	LoadCommit() (*domain.LocalCommit, error)
	SaveStatEntries(entries map[string]domain.StatEntry) error
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

func (p *Push) Run(ctx context.Context, root, message string, progress ...UploadProgress) error {
	if strings.TrimSpace(message) == "" {
		return domain.NewUserError("commit message is required")
	}
	if err := p.localRepo.Init(root); err != nil {
		return err
	}
	cfg, err := p.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if err := p.pushClient.Connect(ctx, nipaUrl.Host); err != nil {
		return err
	}
	if err := p.auth.MakeSureLoggedIn(ctx, nipaUrl.Host); err != nil {
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

	if _, err := p.pushStaged(ctx, root, nipaUrl, cfg.Branch, message, baseTreeHash, baseCommitID, parent2, progress...); err != nil {
		return err
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
	cfg, err := p.localRepo.LoadConfig()
	if err != nil {
		return nil, err
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
	snapshot, err := p.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := p.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	if len(staged) == 0 {
		return nil, domain.NewUserError("nothing staged to push; run nipa add first")
	}
	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}
	toRead, removed, err := classifyStaged(root, staged, baseByPath)
	if err != nil {
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
		case base.Hash != file.FileHash || normalizedMode(base.Mode) != serverModeFromPerm(sf.info.Mode()):
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

func (p *Push) pushStaged(ctx context.Context, root string, nipaUrl *domain.NipaUrl, branch, message, baseTreeHash, baseCommitID, parent2 string, progress ...UploadProgress) (*serverDomain.PushResult, error) {
	snapshot, err := p.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := p.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	if len(staged) == 0 {
		return nil, domain.NewUserError("nothing staged to push; run nipa add first")
	}

	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}

	toRead, removed, err := classifyStaged(root, staged, baseByPath)
	if err != nil {
		return nil, err
	}
	var files []*serverDomain.PushFile
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
		onChunk = func(ch *serverDomain.ChunkData) {
			progressMu.Lock()
			defer progressMu.Unlock()
			doneObjects++
			doneBytes += chunkRawSize(ch)
			prog.UploadProgress(doneObjects, doneBytes)
		}
	}

	uploader := newChunkUploader(ctx, p.pushClient, p.localRepo,
		domain.ChunkScope{Org: nipaUrl.Org, Project: nipaUrl.Project}, onChunk)
	statEntries := make(map[string]domain.StatEntry, len(toRead))
	for _, sf := range toRead {
		file, err := scanPushFile(sf, uploader)
		if err != nil {
			uploader.abort()
			return nil, err
		}
		files = append(files, file)
		statEntries[sf.path] = statEntryFromInfo(sf.info, file.FileHash)
	}
	if err := uploader.close(); err != nil {
		return nil, err
	}
	if prog != nil {
		prog.UploadEnd()
	}
	if len(statEntries) > 0 {
		if err := p.localRepo.SaveStatEntries(statEntries); err != nil {
			return nil, err
		}
	}

	if baseTreeHash == "" {
		baseTreeHash = snapshot.TreeHash
	}
	result, err := p.pushClient.Push(ctx, nipaUrl.Org, nipaUrl.Project, branch, baseTreeHash, message, files, removed, parent2, baseCommitID)
	if err != nil {
		return nil, err
	}
	if err := p.localRepo.SaveCommit(result.CommitID.Base36(), result.CommitHash.String()); err != nil {
		return nil, err
	}

	var sparse []string
	if cfg, err := p.localRepo.LoadConfig(); err == nil && cfg != nil {
		sparse = cfg.Sparse
	}
	rootTree, err := p.pushClient.GetTreeNodeManifest(ctx, nipaUrl.Org, nipaUrl.Project, branch, sparse)
	if err != nil {
		return nil, err
	}
	if err := p.localRepo.SaveTree(rootTree); err != nil {
		return nil, err
	}
	if err := p.localRepo.ClearStaged(); err != nil {
		return nil, err
	}
	return result, nil
}

// uploadWindowBytes is the chunk data buffered before a window is handed to
// the uploaders. The scanner keeps chunking while earlier windows transfer, so
// local hashing overlaps with network I/O.
var uploadWindowBytes = 16 << 20

const (
	uploadWindowWorkers = 2
	uploadWindowQueue   = 2
)

type chunkUploader struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    pushClient
	localRepo pushLocalRepo
	scope     domain.ChunkScope
	onChunk   func(ch *serverDomain.ChunkData)

	seen  map[serverDomain.Hash]bool
	batch []*serverDomain.ChunkData
	bytes int

	windows chan []*serverDomain.ChunkData
	wg      sync.WaitGroup

	mu  sync.Mutex
	err error
}

func newChunkUploader(ctx context.Context, client pushClient, localRepo pushLocalRepo, scope domain.ChunkScope, onChunk func(*serverDomain.ChunkData)) *chunkUploader {
	uploadCtx, cancel := context.WithCancel(ctx)
	u := &chunkUploader{
		ctx:       uploadCtx,
		cancel:    cancel,
		client:    client,
		localRepo: localRepo,
		scope:     scope,
		onChunk:   onChunk,
		seen:      make(map[serverDomain.Hash]bool),
		windows:   make(chan []*serverDomain.ChunkData, uploadWindowQueue),
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
		if err := u.localRepo.StoreChunks(window); err != nil {
			return err
		}
	}
	_, _, err := u.client.UploadChunks(u.ctx, u.scope, window, u.onChunk)
	return err
}

func (u *chunkUploader) add(ch *serverDomain.ChunkData) error {
	if u.ctx.Err() != nil {
		return u.failure()
	}
	if u.seen[ch.Hash] {
		return nil
	}
	u.seen[ch.Hash] = true
	u.batch = append(u.batch, ch)
	u.bytes += len(ch.Data)
	if u.bytes < uploadWindowBytes {
		return nil
	}
	return u.flush()
}

func (u *chunkUploader) flush() error {
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
		switch {
		case err == nil:
			if !info.Mode().IsRegular() {
				return nil, nil, domain.NewUserError(fmt.Sprintf("%q is not a regular file", path))
			}
			cfg, isBinary := profileForFile(path, abs)
			toRead = append(toRead, stagedFile{path: path, info: info, abs: abs, cfg: cfg, isBinary: isBinary})
		case os.IsNotExist(err):
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
	add(ch *serverDomain.ChunkData) error
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

func (c *planCollector) add(ch *serverDomain.ChunkData) error {
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
	encoding, err := chunker.Encode(f, sf.path, sf.isBinary, "", func(c chunker.EncodedChunk) error {
		hashes = append(hashes, c.Hash)
		return sink.add(&serverDomain.ChunkData{Hash: c.Hash, Data: c.Data, RawSize: c.RawSize})
	})
	if err != nil {
		return nil, err
	}
	return &serverDomain.PushFile{
		Path:        sf.path,
		Mode:        int(sf.info.Mode().Perm()),
		SizeBytes:   sf.info.Size(),
		IsBinary:    sf.isBinary,
		Encoding:    encoding,
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}, nil
}
