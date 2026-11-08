package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type downloadingConnector struct {
	fakeConnector
	data  map[serverDomain.Hash][]byte
	calls int
}

func (d *downloadingConnector) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	d.calls++
	for _, h := range hashes {
		if err := onChunk(h, d.data[h]); err != nil {
			return err
		}
	}
	return nil
}

func dropObjects(t *testing.T, root string) map[serverDomain.Hash][]byte {
	t.Helper()
	objects := filepath.Join(root, localrepo.ConfigDir, localrepo.ObjectsDir)
	data := map[serverDomain.Hash][]byte{}
	require.NoError(t, filepath.WalkDir(objects, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		h, err := serverDomain.ParseHashHex(filepath.Base(filepath.Dir(p)) + d.Name())
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		data[h] = b
		return err
	}))
	require.NoError(t, os.RemoveAll(objects))
	require.NoError(t, os.MkdirAll(objects, 0o755))
	return data
}

func TestSetupRestoreCmd_DownloadsMissingContent(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "original"})
	writeFile(t, root, "a.txt", "edited")
	conn := &downloadingConnector{data: dropObjects(t, root)}

	_, err := runCmdInDir(t, root, NewCli(&fakeUsecaseContainer{}, conn).setupRestoreCmd(), "a.txt")
	require.NoError(t, err)
	require.Equal(t, 1, conn.calls)
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}

func TestSetupRestoreCmd_MissingContentWithoutDownloader(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "original"})
	writeFile(t, root, "a.txt", "edited")
	dropObjects(t, root)

	_, err := runCmdInDir(t, root, newStageCli().setupRestoreCmd(), "a.txt")
	require.ErrorContains(t, err, "content not cached locally")
}
