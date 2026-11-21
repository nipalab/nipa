package localrepo

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func requireLinkedMetadata(t *testing.T, err error, rel string) {
	t.Helper()
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Equal(t, 400, de.Code)
	require.ErrorContains(t, err, `refusing to use "`+rel+`": it is a symbolic link`)
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func TestInit_RefusesALinkedMetadataFolder(t *testing.T) {
	for _, rel := range []string{".nipa", ".nipa/objects", ".nipa/writes", ".nipa/shelves"} {
		t.Run(rel, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			symlinkOrSkip(t, outside, filepath.Join(root, filepath.FromSlash(rel)))

			lr := NewLocalRepo()
			t.Cleanup(func() { _ = lr.Close() })
			requireLinkedMetadata(t, lr.Init(root), rel)
			require.Empty(t, entries(t, outside), "nothing is created behind the link")
			require.Nil(t, lr.db)
		})
	}
}

func TestInit_AcceptsALinkedCloneRoot(t *testing.T) {
	real := t.TempDir()
	root := filepath.Join(t.TempDir(), "clone")
	symlinkOrSkip(t, real, root)
	lr := NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.Close())
	require.NoError(t, lr.Init(root), "an existing clone reopens")
	require.DirExists(t, filepath.Join(real, ConfigDir, ObjectsDir))
}

func TestInit_ReportsAnUnreadableMetadataFolder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o644))
	err := checkMetadataDirs(filepath.Join(root, "file", "x"))
	require.Error(t, err)
}

// swapForLink replaces the folder at dir with a link to a fresh outside
// folder holding the same entries, as a process racing an open clone could.
func swapForLink(t *testing.T, dir string, files ...string) string {
	t.Helper()
	outside := t.TempDir()
	for _, f := range files {
		p := filepath.Join(outside, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("keep"), 0o644))
		old := time.Now().Add(-2 * staleTempAge)
		require.NoError(t, os.Chtimes(p, old, old))
	}
	require.NoError(t, os.RemoveAll(dir))
	symlinkOrSkip(t, outside, dir)
	return outside
}

func TestPruneObjects_RefusesALinkedObjectsFolder(t *testing.T) {
	lr := newTestLocalRepo(t)
	data := []byte("chunk")
	hash := chunker.Sum(data)
	hex := hash.String()
	objects := filepath.Join(lr.target, ConfigDir, ObjectsDir)
	outside := swapForLink(t, objects, hex[:2]+"/"+hex[2:], hex[:2]+"/"+tempFilePrefix+"x")

	_, _, err := lr.PruneObjects(map[serverDomain.Hash]struct{}{})
	requireLinkedMetadata(t, err, ".nipa/objects")
	require.FileExists(t, filepath.Join(outside, hex[:2], hex[2:]))
	require.FileExists(t, filepath.Join(outside, hex[:2], tempFilePrefix+"x"))
}

func TestPruneObjects_SkipsALinkedFanOutFolder(t *testing.T) {
	lr := newTestLocalRepo(t)
	data := []byte("chunk")
	hash := chunker.Sum(data)
	hex := hash.String()
	require.NoError(t, lr.StoreChunk(hash, data))
	outside := swapForLink(t, filepath.Join(lr.target, ConfigDir, ObjectsDir, hex[:2]), hex[2:], tempFilePrefix+"x")

	files, _, err := lr.PruneObjects(map[serverDomain.Hash]struct{}{})
	require.NoError(t, err)
	require.Zero(t, files)
	require.FileExists(t, filepath.Join(outside, hex[2:]))
	require.FileExists(t, filepath.Join(outside, tempFilePrefix+"x"))
}

func TestBeginWorkingWrite_RefusesALinkedWritesFolder(t *testing.T) {
	lr := newTestLocalRepo(t)
	end, _, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	end()
	outside := swapForLink(t, lr.writesDir(), "w-stale")

	_, _, err = lr.BeginWorkingWrite()
	requireLinkedMetadata(t, err, ".nipa/writes")
	require.Equal(t, []string{"w-stale"}, entries(t, outside), "no marker is removed or created behind the link")
}

func TestShelves_NeverDeleteThroughALinkedShelvesFolder(t *testing.T) {
	lr := newTestLocalRepo(t)
	shelf, err := lr.CreateShelf(func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		return &domain.Shelf{Name: "s"}, nil
	})
	require.NoError(t, err)
	outside := swapForLink(t, lr.shelvesRoot(), shelf.ID+"/"+shelfManifest, shelfTmpPrefix+"old/x")

	_, err = lr.NewShelfWriter()
	requireLinkedMetadata(t, err, ".nipa/shelves")
	require.FileExists(t, filepath.Join(outside, shelfTmpPrefix+"old", "x"), "the stale temp behind the link is not swept")
	require.Len(t, entries(t, outside), 2, "no shelf is started behind the link")
	requireLinkedMetadata(t, lr.DropShelf(shelf.ID), ".nipa/shelves")
	require.FileExists(t, filepath.Join(outside, shelf.ID, shelfManifest))
}

func TestInit_ReportsMetadataThatCannotBeOpened(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ConfigDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigDir, ObjectsDir), nil, 0o644))
	lr := NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.Error(t, lr.Init(root), "the objects folder is a file")

	root = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ConfigDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigDir, DBFile), []byte("not a database, not at all"), 0o644))
	require.Error(t, lr.Init(root), "the database is not one")
	require.Nil(t, lr.db)

	root = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ConfigDir), 0o755))
	db, err := sql.Open("sqlite", filepath.Join(root, ConfigDir, DBFile))
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE files (other TEXT)")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.Error(t, lr.Init(root), "the schema cannot be applied")
	require.Nil(t, lr.db)
}

func TestNewShelfWriter_ReportsAnUnusableShelvesFolder(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, os.WriteFile(lr.shelvesRoot(), nil, 0o644))
	_, err := lr.NewShelfWriter()
	require.Error(t, err, "the shelves folder is a file")
	require.NoError(t, os.Remove(lr.shelvesRoot()))

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("folder permissions do not refuse writes here")
	}
	require.NoError(t, os.Mkdir(lr.shelvesRoot(), 0o555))
	t.Cleanup(func() { _ = os.Chmod(lr.shelvesRoot(), 0o755) })
	_, err = lr.NewShelfWriter()
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestOpenRealDirRefusesLinkedIntermediate(t *testing.T) {
	target := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(outside, ObjectsDir), 0o755))
	marker := filepath.Join(outside, ObjectsDir, "keep")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o644))
	symlinkOrSkip(t, outside, filepath.Join(target, ConfigDir))

	r, err := openRealDir(target, filepath.Join(ConfigDir, ObjectsDir))
	require.Error(t, err)
	require.Nil(t, r)
	require.Contains(t, err.Error(), ConfigDir)
	require.FileExists(t, marker)
	entries, err := os.ReadDir(filepath.Join(outside, ObjectsDir))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestOpenRealDirRefusesLinkedLeaf(t *testing.T) {
	target := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(target, ConfigDir), 0o755))
	symlinkOrSkip(t, outside, filepath.Join(target, ConfigDir, ObjectsDir))

	r, err := openRealDir(target, filepath.Join(ConfigDir, ObjectsDir))
	require.Error(t, err)
	require.Nil(t, r)
	require.Contains(t, err.Error(), "objects")
}

func TestOpenRealDirOpensRealTreeAndReportsMissing(t *testing.T) {
	target := t.TempDir()
	rel := filepath.Join(ConfigDir, ObjectsDir)
	_, err := openRealDir(target, rel)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = openRealDir(filepath.Join(target, "nope"), rel)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, os.MkdirAll(filepath.Join(target, rel), 0o755))
	r, err := openRealDir(target, rel)
	require.NoError(t, err)
	require.NoError(t, r.WriteFile("f", []byte("x"), 0o644))
	require.NoError(t, r.Close())
	require.FileExists(t, filepath.Join(target, rel, "f"))
}

func TestEnsureRealDirRefusesLinkWithoutCreating(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(l *LocalRepo) error
	}{
		{"writes", func(l *LocalRepo) error { _, _, err := l.BeginWorkingWrite(); return err }},
		{"shelves", func(l *LocalRepo) error { _, err := l.NewShelfWriter(); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			l := &LocalRepo{}
			require.NoError(t, l.Init(root))
			require.NoError(t, os.RemoveAll(filepath.Join(root, ConfigDir, WritesDir)))
			require.NoError(t, os.RemoveAll(filepath.Join(root, ConfigDir, ShelvesDir)))
			outside := t.TempDir()
			moved := filepath.Join(t.TempDir(), "moved")
			require.NoError(t, os.Rename(filepath.Join(root, ConfigDir), moved))
			symlinkOrSkip(t, outside, filepath.Join(root, ConfigDir))
			err := tc.run(l)
			require.Error(t, err)
			require.Contains(t, err.Error(), "symbolic link")
			entries, rerr := os.ReadDir(outside)
			require.NoError(t, rerr)
			require.Empty(t, entries)
		})
	}
}

func TestEnsureRealDirCreatesMissing(t *testing.T) {
	root := t.TempDir()
	l := &LocalRepo{}
	require.NoError(t, l.Init(root))
	end, _, err := l.BeginWorkingWrite()
	require.NoError(t, err)
	end()
	w, err := l.NewShelfWriter()
	require.NoError(t, err)
	w.Abort()
	info, err := os.Lstat(filepath.Join(root, ConfigDir, WritesDir))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestObjectWrites_RefuseLinkedFolders(t *testing.T) {
	hash := chunker.Sum([]byte("payload"))
	hex := hash.String()
	cases := []struct{ name, rel string }{
		{"nipa", ConfigDir},
		{"objects", filepath.Join(ConfigDir, ObjectsDir)},
		{"fanout", filepath.Join(ConfigDir, ObjectsDir, hex[:2])},
	}
	ops := map[string]func(*LocalRepo) error{
		"putObject": func(l *LocalRepo) error { return l.putObject(hash, []byte("payload")) },
		"StoreChunkFrom": func(l *LocalRepo) error {
			_, err := l.StoreChunkFrom(hash, strings.NewReader("payload"))
			return err
		},
		"MarkInterruptedWrite": func(l *LocalRepo) error { return l.MarkInterruptedWrite() },
	}
	for _, tc := range cases {
		for opName, op := range ops {
			if opName == "MarkInterruptedWrite" && tc.name != "nipa" {
				continue
			}
			t.Run(tc.name+"/"+opName, func(t *testing.T) {
				root := t.TempDir()
				l := &LocalRepo{}
				require.NoError(t, l.Init(root))
				t.Cleanup(func() { _ = l.Close() })
				link := filepath.Join(root, tc.rel)
				require.NoError(t, os.MkdirAll(link, 0o755))
				require.NoError(t, os.Rename(link, filepath.Join(t.TempDir(), "moved")))
				outside := t.TempDir()
				symlinkOrSkip(t, outside, link)
				err := op(l)
				requireLinkedMetadata(t, err, tc.rel)
				entries, rerr := os.ReadDir(outside)
				require.NoError(t, rerr)
				require.Empty(t, entries)
			})
		}
	}
}

func TestObjectWrites_NormalPaths(t *testing.T) {
	root := t.TempDir()
	l := &LocalRepo{}
	require.NoError(t, l.Init(root))
	t.Cleanup(func() { _ = l.Close() })
	data := []byte("payload")
	hash := chunker.Sum(data)
	require.NoError(t, l.putObject(hash, data))
	require.NoError(t, l.putObject(hash, data), "an existing object is kept")
	n, err := l.StoreChunkFrom(hash, strings.NewReader("payload"))
	require.NoError(t, err)
	require.EqualValues(t, len(data), n)

	other := []byte("other payload")
	oh := chunker.Sum(other)
	n, err = l.StoreChunkFrom(oh, bytes.NewReader(other))
	require.NoError(t, err)
	require.EqualValues(t, len(other), n)
	_, err = l.StoreChunkFrom(oh, strings.NewReader("tampered"))
	require.NoError(t, err)
	bad := chunker.Sum([]byte("x"))
	_, err = l.StoreChunkFrom(bad, strings.NewReader("y"))
	require.ErrorContains(t, err, "hash mismatch")

	rc, err := l.OpenChunk(oh)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	require.Equal(t, other, got)
	require.NoError(t, l.MarkInterruptedWrite())
	require.FileExists(t, filepath.Join(root, ConfigDir, WritesDir, interruptedMarker))
	matches, _ := filepath.Glob(filepath.Join(root, ConfigDir, ObjectsDir, "*", tempFilePrefix+"*"))
	require.Empty(t, matches)
}
