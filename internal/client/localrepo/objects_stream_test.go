package localrepo

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func objectFiles(t *testing.T, lr *LocalRepo) []string {
	t.Helper()
	var names []string
	root := filepath.Join(lr.target, ConfigDir, ObjectsDir)
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			names = append(names, d.Name())
		}
		return err
	}))
	return names
}

func TestStoreChunkFrom_StoresVerifiedContent(t *testing.T) {
	lr := newTestLocalRepo(t)
	data := bytes.Repeat([]byte("streamed object "), 40000)
	hash := chunker.Sum(data)

	n, err := lr.StoreChunkFrom(hash, bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), n)
	stored, err := lr.LoadChunk(hash)
	require.NoError(t, err)
	require.Equal(t, data, stored)

	n, err = lr.StoreChunkFrom(hash, bytes.NewReader(data))
	require.NoError(t, err, "an object that is already stored is left alone")
	require.Equal(t, int64(len(data)), n, "its body is still consumed")
	require.Len(t, objectFiles(t, lr), 1)
}

func TestStoreChunkFrom_RejectsWrongOrBrokenContent(t *testing.T) {
	lr := newTestLocalRepo(t)
	hash := chunker.Sum([]byte("expected"))

	_, err := lr.StoreChunkFrom(hash, bytes.NewReader([]byte("something else")))
	require.ErrorContains(t, err, "chunk hash mismatch")

	broken := errors.New("connection reset")
	_, err = lr.StoreChunkFrom(hash, io.MultiReader(bytes.NewReader([]byte("expe")), iotest.ErrReader(broken)))
	require.ErrorIs(t, err, broken)

	require.Empty(t, objectFiles(t, lr), "neither the object nor a temp file is left behind")
	missing, err := lr.MissingChunks([]serverDomain.Hash{hash})
	require.NoError(t, err)
	require.Len(t, missing, 1)

	_, err = NewLocalRepo().StoreChunkFrom(hash, bytes.NewReader(nil))
	require.Error(t, err, "an unopened repository cannot store")
}
