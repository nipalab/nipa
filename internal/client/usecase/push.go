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
	Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string) (*serverDomain.PushResult, error)
	UploadChunks(ctx context.Context, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error)
}

type pushLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	ClearStaged() error
	SaveTree(root *serverDomain.TreeNode) error
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

	var files []*serverDomain.PushFile
	var removed []string
	var allChunkData []*serverDomain.ChunkData
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
			file, chunkData, err := buildPushFile(path, info, abs)
			if err != nil {
				return err
			}
			files = append(files, file)
			allChunkData = append(allChunkData, chunkData...)
		case os.IsNotExist(err):
			if _, inBase := baseByPath[path]; !inBase {
				return domain.NewUserError(fmt.Sprintf("staged file %q does not exist", path))
			}
			removed = append(removed, path)
		default:
			return err
		}
	}

	missing, err := p.localRepo.MissingChunks(allChunkHashes(allChunkData))
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		toUpload := dedupeChunkData(allChunkData, missing)
		var prog UploadProgress
		if len(progress) > 0 {
			prog = progress[0]
		}
		if prog != nil {
			var totalBytes int64
			for _, c := range toUpload {
				totalBytes += int64(len(c.Data))
			}
			prog.UploadStart(len(toUpload), totalBytes)
		}

		doneObjects, doneBytes := 0, int64(0)
		var onChunk func(ch *serverDomain.ChunkData)
		if prog != nil {
			onChunk = func(ch *serverDomain.ChunkData) {
				doneObjects++
				doneBytes += int64(len(ch.Data))
				prog.UploadProgress(doneObjects, doneBytes)
			}
		}

		if _, _, err := p.pushClient.UploadChunks(ctx, toUpload, onChunk); err != nil {
			return err
		}
		if prog != nil {
			prog.UploadEnd()
		}
	}

	if _, err := p.pushClient.Push(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch, snapshot.TreeHash, message, files, removed); err != nil {
		return err
	}

	rootTree, err := p.pushClient.GetTreeNodeManifest(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch, "")
	if err != nil {
		return err
	}
	if err := p.localRepo.SaveTree(rootTree); err != nil {
		return err
	}
	return p.localRepo.ClearStaged()
}

func buildPushFile(path string, info fs.FileInfo, abs string) (*serverDomain.PushFile, []*serverDomain.ChunkData, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	chunks, err := chunker.ChunkAll(data)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]serverDomain.Hash, len(chunks))
	chunkData := make([]*serverDomain.ChunkData, 0, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		chunkData = append(chunkData, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
	}
	return &serverDomain.PushFile{
		Path:        path,
		Mode:        int(info.Mode().Perm()),
		SizeBytes:   info.Size(),
		IsBinary:    chunker.IsBinary(data),
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}, chunkData, nil
}

func allChunkHashes(chunkData []*serverDomain.ChunkData) []serverDomain.Hash {
	hashes := make([]serverDomain.Hash, len(chunkData))
	for i, c := range chunkData {
		hashes[i] = c.Hash
	}
	return hashes
}

func dedupeChunkData(all []*serverDomain.ChunkData, missing []serverDomain.Hash) []*serverDomain.ChunkData {
	want := make(map[serverDomain.Hash]bool, len(missing))
	for _, h := range missing {
		want[h] = true
	}
	seen := make(map[serverDomain.Hash]bool, len(missing))
	var out []*serverDomain.ChunkData
	for _, c := range all {
		if !want[c.Hash] || seen[c.Hash] {
			continue
		}
		seen[c.Hash] = true
		out = append(out, c)
	}
	return out
}
