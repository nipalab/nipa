package usecase

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

type updateClient interface {
	Connect(ctx context.Context, host string) error
	GetBranchByName(ctx context.Context, org, project, name string) (*domain.Branch, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*domain.TreeNode, error)
	DownloadChunks(ctx context.Context, hashes []domain.Hash, onChunk func(h domain.Hash, data []byte) error) error
}

type chunkDownloader interface {
	DownloadChunks(ctx context.Context, hashes []domain.Hash, onChunk func(h domain.Hash, data []byte) error) error
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

type workingCopyLocalRepo interface {
	Snapshot() (*clientDomain.Snapshot, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunks(chunks []*domain.ChunkData) error
	OpenChunk(hash domain.Hash) (io.ReadCloser, error)
	LoadChunk(hash domain.Hash) ([]byte, error)
}

type updateLocalRepo interface {
	Init(target string) error
	LoadConfig() (*clientDomain.Config, error)
	SaveConfig(cfg clientDomain.Config) error
	ListStaged() ([]string, error)
	SaveCommit(commitID, commitHash string) error
	Snapshot() (*clientDomain.Snapshot, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunks(chunks []*domain.ChunkData) error
	OpenChunk(hash domain.Hash) (io.ReadCloser, error)
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

func (u *Update) Run(ctx context.Context, root string, progress ...DownloadProgress) error {
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

	tree, err := u.client.GetTreeNodeManifest(ctx, nu.Org, nu.Project, cfg.Branch, nil)
	if err != nil {
		return err
	}
	if err := syncWorkingCopy(ctx, u.client, u.localRepo, root, tree, progress...); err != nil {
		return err
	}
	if err := u.localRepo.SaveTree(tree); err != nil {
		return err
	}
	return u.pinHead(ctx, nu, cfg.Branch)
}

func (u *Update) pinHead(ctx context.Context, nu *clientDomain.NipaUrl, branch string) error {
	info, err := u.client.GetBranchByName(ctx, nu.Org, nu.Project, branch)
	if err != nil {
		return err
	}
	if info == nil || info.CommitID == nil {
		return nil
	}
	return u.localRepo.SaveCommit(info.CommitID.Base36(), "")
}

func (u *Update) Switch(ctx context.Context, root, branch string, progress ...DownloadProgress) error {
	if err := u.localRepo.Init(root); err != nil {
		return err
	}
	cfg, err := u.localRepo.LoadConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(branch) == "" {
		return clientDomain.NewUserError("branch name is required")
	}
	if branch == cfg.Branch {
		return nil
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if nu.Path != "" {
		return clientDomain.NewUserError("switching branches in a subdirectory clone is not supported yet")
	}
	staged, err := u.localRepo.ListStaged()
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		return clientDomain.NewUserError("cannot switch branches with staged changes; push or reset them first")
	}
	if err := u.client.Connect(ctx, nu.Host); err != nil {
		return err
	}
	if err := u.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}

	tree, err := u.client.GetTreeNodeManifest(ctx, nu.Org, nu.Project, branch, nil)
	if err != nil {
		return err
	}
	if err := syncWorkingCopy(ctx, u.client, u.localRepo, root, tree, progress...); err != nil {
		return err
	}
	if err := u.localRepo.SaveTree(tree); err != nil {
		return err
	}
	if err := u.localRepo.SaveConfig(clientDomain.Config{Url: cfg.Url, Branch: branch}); err != nil {
		return err
	}
	return u.pinHead(ctx, nu, branch)
}

func syncWorkingCopy(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, root string, tree *domain.TreeNode, progress ...DownloadProgress) error {
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
	if err := downloadMissing(ctx, client, lr, missing, estimateBytesToDownload(newFiles, missing), progress...); err != nil {
		return err
	}

	for _, f := range newFiles {
		if base, ok := baseByPath[f.Path]; ok && base.Hash == f.FileHash && base.Mode == f.Mode && fileExists(root, f.Path) {
			continue
		}
		if err := materializeFile(root, f, lr.OpenChunk); err != nil {
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

var chunkStoreBatchBytes = 8 << 20

func downloadMissing(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, missing []domain.Hash, estimatedBytes int64, progress ...DownloadProgress) error {
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

	doneObjects, doneBytes := 0, int64(0)
	seen := make(map[domain.Hash]bool, len(missing))
	pending := make([]*domain.ChunkData, 0, 64)
	pendingBytes := 0
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := lr.StoreChunks(pending); err != nil {
			return err
		}
		pending = nil
		pendingBytes = 0
		return nil
	}

	err := client.DownloadChunks(ctx, missing, func(h domain.Hash, data []byte) error {
		if seen[h] {
			return nil
		}
		seen[h] = true
		if chunker.Sum(data) != h {
			return fmt.Errorf("chunk hash mismatch for %s", h)
		}
		pending = append(pending, &domain.ChunkData{Hash: h, Data: data})
		pendingBytes += len(data)
		if prog != nil {
			doneObjects++
			doneBytes += int64(len(data))
			prog.DownloadProgress(doneObjects, doneBytes)
		}
		if pendingBytes >= chunkStoreBatchBytes {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, h := range missing {
		if !seen[h] {
			return fmt.Errorf("server did not return chunk %s", h)
		}
	}
	if err := flush(); err != nil {
		return err
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

func estimateBytesToDownload(files []materializedFile, missing []domain.Hash) int64 {
	missingSet := make(map[domain.Hash]struct{}, len(missing))
	for _, h := range missing {
		missingSet[h] = struct{}{}
	}
	var total int64
	for _, f := range files {
		for _, h := range f.ChunkHashes {
			if _, ok := missingSet[h]; ok {
				total += f.SizeBytes
				break
			}
		}
	}
	return total
}

func fileExists(root, path string) bool {
	fp := filepath.Join(root, filepath.FromSlash(path))
	_, err := os.Stat(fp)
	return err == nil
}

func materializeFile(root string, f materializedFile, openChunk func(domain.Hash) (io.ReadCloser, error)) error {
	fp := filepath.Join(root, filepath.FromSlash(f.Path))
	dir := filepath.Dir(fp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nipa-materialize-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	var written int64
	for _, h := range f.ChunkHashes {
		rc, err := openChunk(h)
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("load chunk %s: %w", h, err)
		}
		n, err := io.Copy(tmp, io.LimitReader(rc, f.SizeBytes-written+1))
		_ = rc.Close()
		if err != nil {
			_ = tmp.Close()
			return err
		}
		written += n
		if written > f.SizeBytes {
			_ = tmp.Close()
			return fmt.Errorf("content length mismatch for %s: got %d want %d", f.Path, written, f.SizeBytes)
		}
	}
	if written != f.SizeBytes {
		_ = tmp.Close()
		return fmt.Errorf("content length mismatch for %s: got %d want %d", f.Path, written, f.SizeBytes)
	}
	if err := tmp.Chmod(materializedMode(f.Mode)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, fp)
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
	f, err := os.Open(fp)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	var hashes []domain.Hash
	if err := chunker.Scan(f, func(c chunker.Chunk) error {
		hashes = append(hashes, c.Hash)
		return nil
	}); err != nil {
		return false, err
	}
	if chunker.FileHash(hashes) == base.Hash {
		return true, os.Remove(fp)
	}
	return false, nil
}
