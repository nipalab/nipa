package usecase

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type pushClient interface {
	Connect(ctx context.Context, host string) error
	Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash string) (*serverDomain.PushResult, error)
	UploadChunks(ctx context.Context, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error)
}

type pushLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	ClearStaged() error
	SaveTree(root *serverDomain.TreeNode) error
	SaveCommit(commitID, commitHash string) error
	LoadMergeState() (*domain.MergeState, error)
	ClearMergeState() error
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
	if nipaUrl.Path != "" {
		return domain.NewUserError("pushing from a subdirectory clone is not supported yet")
	}
	if err := p.pushClient.Connect(ctx, nipaUrl.Host); err != nil {
		return err
	}
	if err := p.auth.MakeSureLoggedIn(ctx, nipaUrl.Host); err != nil {
		return err
	}

	snapshot, err := p.localRepo.Snapshot()
	if err != nil {
		return err
	}
	staged, err := p.localRepo.ListStaged()
	if err != nil {
		return err
	}
	if len(staged) == 0 {
		return domain.NewUserError("nothing staged to push; run nipa add first")
	}

	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}

	type stagedFile struct {
		path string
		info fs.FileInfo
		abs  string
	}

	var files []*serverDomain.PushFile
	var removed []string
	var toRead []stagedFile
	var estBytes int64
	for _, path := range staged {
		if isNipaPath(path) {
			return domain.NewUserError(fmt.Sprintf("cannot push path inside %q: %s", nipaDir, path))
		}
		abs := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Stat(abs)
		switch {
		case err == nil:
			if !info.Mode().IsRegular() {
				return domain.NewUserError(fmt.Sprintf("%q is not a regular file", path))
			}
			toRead = append(toRead, stagedFile{path: path, info: info, abs: abs})
			estBytes += info.Size()
		case os.IsNotExist(err):
			if _, inBase := baseByPath[path]; !inBase {
				return domain.NewUserError(fmt.Sprintf("staged file %q does not exist", path))
			}
			removed = append(removed, path)
		default:
			return err
		}
	}

	var prog UploadProgress
	if len(progress) > 0 {
		prog = progress[0]
	}
	doneObjects, doneBytes := 0, int64(0)
	var onChunk func(ch *serverDomain.ChunkData)
	if prog != nil {
		estObjects := 0
		if estBytes > 0 {
			estObjects = int(estBytes / chunker.DefaultConfig.Avg)
			if estObjects < 1 {
				estObjects = 1
			}
		}
		prog.UploadStart(estObjects, estBytes)
		onChunk = func(ch *serverDomain.ChunkData) {
			doneObjects++
			doneBytes += int64(len(ch.Data))
			prog.UploadProgress(doneObjects, doneBytes)
		}
	}

	batcher := &chunkBatcher{
		ctx:     ctx,
		client:  p.pushClient,
		seen:    make(map[serverDomain.Hash]bool),
		onChunk: onChunk,
	}
	for _, sf := range toRead {
		file, err := scanPushFile(sf.path, sf.info, sf.abs, batcher)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	if err := batcher.flush(); err != nil {
		return err
	}
	if prog != nil {
		prog.UploadEnd()
	}

	var parent2 string
	mergeState, err := p.localRepo.LoadMergeState()
	if err != nil {
		return err
	}
	baseTreeHash := snapshot.TreeHash
	if mergeState != nil {
		parent2 = mergeState.SourceCommitHash
		if mergeState.TargetTreeHash != "" {
			baseTreeHash = mergeState.TargetTreeHash
		}
	}
	result, err := p.pushClient.Push(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch, baseTreeHash, message, files, removed, parent2)
	if err != nil {
		return err
	}
	if err := p.localRepo.SaveCommit(result.CommitID.Base36(), result.CommitHash.String()); err != nil {
		return err
	}

	rootTree, err := p.pushClient.GetTreeNodeManifest(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch, "")
	if err != nil {
		return err
	}
	if err := p.localRepo.SaveTree(rootTree); err != nil {
		return err
	}
	if err := p.localRepo.ClearStaged(); err != nil {
		return err
	}
	return p.localRepo.ClearMergeState()
}

var uploadBatchBytes = 8 << 20

type chunkBatcher struct {
	ctx     context.Context
	client  pushClient
	seen    map[serverDomain.Hash]bool
	batch   []*serverDomain.ChunkData
	bytes   int
	onChunk func(ch *serverDomain.ChunkData)
}

func (b *chunkBatcher) add(ch *serverDomain.ChunkData) error {
	if b.seen[ch.Hash] {
		return nil
	}
	b.seen[ch.Hash] = true
	b.batch = append(b.batch, ch)
	b.bytes += len(ch.Data)
	if b.bytes < uploadBatchBytes {
		return nil
	}
	return b.flush()
}

func (b *chunkBatcher) flush() error {
	if len(b.batch) == 0 {
		return nil
	}
	_, _, err := b.client.UploadChunks(b.ctx, b.batch, b.onChunk)
	b.batch = nil
	b.bytes = 0
	return err
}

func scanPushFile(path string, info fs.FileInfo, abs string, batcher *chunkBatcher) (*serverDomain.PushFile, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var hashes []serverDomain.Hash
	isBinary := false
	err = chunker.Scan(f, func(c chunker.Chunk) error {
		hashes = append(hashes, c.Hash)
		if len(hashes) == 1 {
			isBinary = chunker.IsBinary(c.Data)
		}
		return batcher.add(&serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
	})
	if err != nil {
		return nil, err
	}
	return &serverDomain.PushFile{
		Path:        path,
		Mode:        int(info.Mode().Perm()),
		SizeBytes:   info.Size(),
		IsBinary:    isBinary,
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}, nil
}
