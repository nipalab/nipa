package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// leaveTemp plants a materialize temp file last written age ago.
func leaveTemp(t *testing.T, root, rel string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("half a file"), 0o600))
	at := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, at, at))
	return path
}

func leaveDeadWriteMarker(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".nipa", localrepo.WritesDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "w-dead"), nil, 0o644))
}

func TestSweepMaterializeTemps(t *testing.T) {
	root := t.TempDir()
	old := leaveTemp(t, root, "assets/.nipa-materialize-111", time.Hour)
	young := leaveTemp(t, root, ".nipa-materialize-222", 0)
	inMeta := leaveTemp(t, root, ".nipa/objects/.nipa-materialize-333", time.Hour)
	other := leaveTemp(t, root, "assets/texture.png", time.Hour)
	require.NoError(t, os.Mkdir(filepath.Join(root, "assets", ".nipa-materialize-dir"), 0o755))

	removed, left := sweepMaterializeTemps(root, time.Now().Add(-time.Minute))
	require.Equal(t, 1, removed)
	require.Equal(t, 1, left)
	require.NoFileExists(t, old)
	require.FileExists(t, young)
	require.FileExists(t, inMeta, "the metadata directory is never swept")
	require.FileExists(t, other)

	removed, left = sweepMaterializeTemps(filepath.Join(root, "missing"), time.Now())
	require.Zero(t, removed+left)
}

func TestSync_SweepsTempFilesOfAKilledWrite(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("a.bin", data, "AAAA")}}
	leftover := leaveTemp(t, root, "dir/.nipa-materialize-999", time.Minute)

	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))
	require.FileExists(t, leftover, "without a dead marker nothing walks the working copy")

	leaveDeadWriteMarker(t, root)
	fresh := leaveTemp(t, root, "dir/.nipa-materialize-fresh", 0)
	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))
	require.NoFileExists(t, leftover, "the next write after a killed one sweeps its temp files")
	require.FileExists(t, fresh, "a temp file as young as this write may belong to another process")

	at := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes(fresh, at, at))
	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))
	require.NoFileExists(t, fresh, "what was too young is swept by a later write")
}

func TestRestore_SweepsTempFilesOfAKilledWrite(t *testing.T) {
	d := newDirectRestore(t)
	leftover := leaveTemp(t, d.root, ".nipa-materialize-999", time.Minute)
	leaveDeadWriteMarker(t, d.root)
	d.edit(t)

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))
	require.NoFileExists(t, leftover)
}

type failingMarker struct{ markErr error }

func (failingMarker) BeginWorkingWrite() (func(), bool, error) {
	return nil, false, errors.New("marker unavailable")
}
func (f failingMarker) MarkInterruptedWrite() error { return f.markErr }

type interruptedMarker struct {
	failingMarker
	ended, remarked int
}

func (m *interruptedMarker) BeginWorkingWrite() (func(), bool, error) {
	return func() { m.ended++ }, true, nil
}

func (m *interruptedMarker) MarkInterruptedWrite() error {
	m.remarked++
	return m.markErr
}

func TestBeginWorkingWrite_Degrades(t *testing.T) {
	root := t.TempDir()
	beginWorkingWrite(struct{}{}, root)()
	beginWorkingWrite(failingMarker{}, root)()

	leaveTemp(t, root, ".nipa-materialize-young", 0)
	marker := &interruptedMarker{failingMarker: failingMarker{markErr: errors.New("read-only")}}
	beginWorkingWrite(marker, root)()
	require.Equal(t, 1, marker.remarked, "leftovers too young to remove keep the sweep pending")
	require.Equal(t, 1, marker.ended)
}

func TestGC_SweepsOldTempFiles(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	old := leaveTemp(t, root, "a/.nipa-materialize-old", 2*time.Hour)
	recent := leaveTemp(t, root, "a/.nipa-materialize-recent", time.Minute)

	_, err := NewGC(lr).Run(root)
	require.NoError(t, err)
	require.NoFileExists(t, old)
	require.FileExists(t, recent, "gc cannot tell whether a recent temp file is in use")
}
