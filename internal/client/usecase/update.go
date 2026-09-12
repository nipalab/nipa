package usecase

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

type updateClient interface {
	Connect(ctx context.Context, host string) error
	GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*domain.TreeNode, error)
	DownloadChunks(ctx context.Context, hashes []domain.Hash) (map[domain.Hash][]byte, error)
}

type chunkDownloader interface {
	DownloadChunks(ctx context.Context, hashes []domain.Hash) (map[domain.Hash][]byte, error)
}

type workingCopyLocalRepo interface {
	Snapshot() (*clientDomain.Snapshot, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunk(hash domain.Hash, data []byte) error
	LoadChunk(hash domain.Hash) ([]byte, error)
}

type updateLocalRepo interface {
	Init(target string) error
	LoadConfig() (*clientDomain.Config, error)
	Snapshot() (*clientDomain.Snapshot, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunk(hash domain.Hash, data []byte) error
	LoadChunk(hash domain.Hash) ([]byte, error)
	SaveTree(root *domain.TreeNode) error
}

type Update struct {
	auth      *Auth
	client    updateClient
	localRepo updateLocalRepo
}

func NewUpdate(auth *Auth, client updateClient, localRepo updateLocalRepo) *Update {
	return &Update{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
	}
}

func (u *Update) Run(ctx context.Context, root string) error {
	if err := u.localRepo.Init(root); err != nil {
		return err
	}
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if nu.Path != "" {
		return clientDomain.NewUserError("update from a subdirectory clone is not supported yet")
	}
	if err := u.client.Connect(ctx, nu.Host); err != nil {
		return err
	}
	if err := u.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}

	base, err := u.localRepo.Snapshot()
	if err != nil {
		return err
	}
	baseByPath := make(map[string]clientDomain.SnapshotFile, len(base.Files))
	for _, f := range base.Files {
		baseByPath[f.Path] = f
	}

	tree, err := u.client.GetTreeNodeManifest(ctx, nu.Org, nu.Project, cfg.Branch, "")
	if err != nil {
		return err
	}
	if err := syncWorkingCopy(ctx, u.client, u.localRepo, root, tree); err != nil {
		return err
	}

	return u.localRepo.SaveTree(tree)
}

func syncWorkingCopy(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, root string, tree *domain.TreeNode) error {
	base, err := lr.Snapshot()
	if err != nil {
		return err
	}
	baseByPath := make(map[string]clientDomain.SnapshotFile, len(base.Files))
	for _, f := range base.Files {
		baseByPath[f.Path] = f
	}

	newFiles := flattenTree(tree, "")
	newByPath := make(map[string]materializedFile, len(newFiles))

	var want []domain.Hash
	for _, f := range newFiles {
		newByPath[f.Path] = f
		want = append(want, f.ChunkHashes...)
	}

	missing, err := lr.MissingChunks(want)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		got, err := client.DownloadChunks(ctx, missing)
		if err != nil {
			return err
		}
		for _, h := range missing {
			data, ok := got[h]
			if !ok {
				return fmt.Errorf("server did not return chunk %s", h)
			}
			if chunker.Sum(data) != h {
				return fmt.Errorf("chunk hash mismatch for %s", h)
			}
			if err := lr.StoreChunk(h, data); err != nil {
				return err
			}
		}
	}

	for _, f := range newFiles {
		if base, ok := baseByPath[f.Path]; ok && base.Hash == f.FileHash && base.Mode == f.Mode && fileExists(root, f.Path) {
			continue
		}
		if err := materializeFile(root, f, lr.LoadChunk); err != nil {
			return err
		}
	}

	for path, base := range baseByPath {
		if _, present := newByPath[path]; present {
			continue
		}
		if _, err := guardedRemove(root, base); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}

	return nil
}

type materializedFile struct {
	Path        string
	Mode        int
	SizeBytes   int64
	FileHash    domain.Hash
	ChunkHashes []domain.Hash
}

func flattenTree(node *domain.TreeNode, prefix string) []materializedFile {
	if node == nil {
		return nil
	}
	var files []materializedFile
	for _, f := range node.FileChildren {
		hashes := make([]domain.Hash, 0, len(f.Chunks))
		for _, c := range f.Chunks {
			hashes = append(hashes, c.Hash)
		}
		path := strings.TrimPrefix(prefix+"/"+f.Name, "/")
		files = append(files, materializedFile{
			Path:        path,
			Mode:        f.Mode,
			SizeBytes:   f.SizeBytes,
			FileHash:    chunker.FileHash(hashes),
			ChunkHashes: hashes,
		})
	}
	for _, child := range node.TreeChildren {
		files = append(files, flattenTree(child, prefix+"/"+child.Name)...)
	}
	return files
}

func fileExists(root, path string) bool {
	fp := filepath.Join(root, filepath.FromSlash(path))
	_, err := os.Stat(fp)
	return err == nil
}

func materializeFile(root string, f materializedFile, loadChunk func(domain.Hash) ([]byte, error)) error {
	var buf bytes.Buffer
	for _, h := range f.ChunkHashes {
		data, err := loadChunk(h)
		if err != nil {
			return fmt.Errorf("load chunk %s: %w", h, err)
		}
		buf.Write(data)
	}
	content := buf.Bytes()
	if int64(len(content)) != f.SizeBytes {
		return fmt.Errorf("content length mismatch for %s: got %d want %d", f.Path, len(content), f.SizeBytes)
	}

	fp := filepath.Join(root, filepath.FromSlash(f.Path))
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fp, content, materializedMode(f.Mode))
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
	fp := filepath.Join(root, filepath.FromSlash(base.Path))
	data, err := os.ReadFile(fp)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	chunks, err := chunker.ChunkAll(data)
	if err != nil {
		return false, err
	}
	hashes := make([]domain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}
	if chunker.FileHash(hashes) == base.Hash {
		return true, os.Remove(fp)
	}
	return false, nil
}
