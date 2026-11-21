//go:build windows

package localrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func mklinkJunction(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	require.NoError(t, err, "mklink /J: %s", out)
}

func TestInit_RefusesAJunctionedMetadataFolder(t *testing.T) {
	for _, rel := range []string{".nipa", ".nipa/objects", ".nipa/writes", ".nipa/shelves"} {
		t.Run(rel, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			mklinkJunction(t, outside, filepath.Join(root, filepath.FromSlash(rel)))

			lr := NewLocalRepo()
			t.Cleanup(func() { _ = lr.Close() })
			err := lr.Init(root)
			var de *domain.Error
			require.ErrorAs(t, err, &de)
			require.Equal(t, 400, de.Code)
			require.ErrorContains(t, err, `refusing to use "`+rel+`": it is a junction or reparse point`)
			require.Empty(t, entries(t, outside))
		})
	}
}

func TestPruneObjects_RefusesAJunctionedObjectsFolder(t *testing.T) {
	lr := newTestLocalRepo(t)
	objects := filepath.Join(lr.target, ConfigDir, ObjectsDir)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o644))
	require.NoError(t, os.RemoveAll(objects))
	mklinkJunction(t, outside, objects)

	_, _, err := lr.PruneObjects(map[serverDomain.Hash]struct{}{})
	require.ErrorContains(t, err, `refusing to use ".nipa/objects": it is a junction or reparse point`)
	require.Equal(t, []string{"keep"}, entries(t, outside))
}
