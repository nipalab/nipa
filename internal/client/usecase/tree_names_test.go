package usecase

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestSyncWorkingCopy_RefusesUnsafeNames(t *testing.T) {
	cases := map[string]func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode{
		"parent file": func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
			return &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("../escaped.bin", data, "evil")}}
		},
		"nested dot dot dirs": func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
			return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{
				Name: "a",
				TreeChildren: []*serverDomain.TreeNode{{
					Name: "..",
					TreeChildren: []*serverDomain.TreeNode{{
						Name:         "..",
						FileChildren: []*serverDomain.File{rawManifestFile("escaped.bin", data, "evil")},
					}},
				}},
			}}}
		},
		"slash in file name": func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
			return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{
				Name:         "sub",
				FileChildren: []*serverDomain.File{rawManifestFile("x/../../../escaped.bin", data, "evil")},
			}}}
		},
		"metadata config": func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
			return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{
				Name:         ".nipa",
				FileChildren: []*serverDomain.File{rawManifestFile("config", data, "evil")},
			}}}
		},
		"metadata config upper case": func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
			return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{
				Name:         ".NIPA",
				FileChildren: []*serverDomain.File{rawManifestFile("config", data, "evil")},
			}}}
		},
		"nested metadata dir": func(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
			return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{
				Name:         "sub",
				TreeChildren: []*serverDomain.TreeNode{{Name: ".Nipa", FileChildren: []*serverDomain.File{rawManifestFile("x", data, "evil")}}},
			}}}
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			lr, root := newDirectRepo(t)
			require.NoError(t, lr.SaveConfig(domain.Config{Branch: "main"}))
			configPath := filepath.Join(root, ".nipa", "config")
			before, err := os.ReadFile(configPath)
			require.NoError(t, err)

			data := map[serverDomain.Hash][]byte{}
			tree := build(data)
			// A safe file listed first proves validation runs before any write.
			tree.FileChildren = append([]*serverDomain.File{rawManifestFile("ok.bin", data, "fine")}, tree.FileChildren...)
			client := &recordingDownloader{data: data}

			err = syncTree(t, lr, root, client, tree)
			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Equal(t, 400, domErr.Code)
			require.Contains(t, domErr.Message, "refusing to sync unsafe path")
			require.NotEmpty(t, domErr.Hint)

			require.Empty(t, client.requested, "nothing is downloaded")
			require.NoFileExists(t, filepath.Join(root, "ok.bin"))
			require.NoFileExists(t, filepath.Join(filepath.Dir(root), "escaped.bin"))
			require.NoFileExists(t, filepath.Join(root, "escaped.bin"))
			after, err := os.ReadFile(configPath)
			require.NoError(t, err)
			require.Equal(t, before, after, ".nipa/config is unchanged")
			requireNoTempFiles(t, root)
			requireNoTempFiles(t, filepath.Dir(root))
		})
	}
}

func TestValidateSyncTree(t *testing.T) {
	require.NoError(t, validateSyncTree(nil))
	ok := &serverDomain.TreeNode{
		Name:         "..",
		FileChildren: []*serverDomain.File{nil, {Name: "a"}},
		TreeChildren: []*serverDomain.TreeNode{nil, {Name: "d", FileChildren: []*serverDomain.File{{Name: ".nipaignore"}}}},
	}
	require.NoError(t, validateSyncTree(ok), "the root name is only a label")

	bad := &serverDomain.TreeNode{TreeChildren: []*serverDomain.TreeNode{{Name: "d", TreeChildren: []*serverDomain.TreeNode{{Name: "."}}}}}
	require.ErrorContains(t, validateSyncTree(bad), `unsafe path "d/."`)
}

func TestMaterializeWritersRefuseUnsafePaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "wc")
	require.NoError(t, os.Mkdir(root, 0o755))
	for _, p := range []string{"../escaped.bin", "a/../../escaped.bin", ".nipa/config", "a/.NIPA/x", ""} {
		f := materializedFile{Path: p, SizeBytes: 1}
		require.ErrorContains(t, materializeFile(root, f, 0, nil), "refusing to sync unsafe path", p)
		_, err := createSizedTemp(root, f)
		require.ErrorContains(t, err, "refusing to sync unsafe path", p)
	}
	require.NoFileExists(t, filepath.Join(parent, "escaped.bin"))
	requireNoTempFiles(t, parent)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestIsNipaPath_CaseInsensitive(t *testing.T) {
	for _, p := range []string{".nipa", ".nipa/config", "a/.NIPA/b", ".Nipa", "NIPA~1/config"} {
		require.True(t, isNipaPath(p), p)
	}
	for _, p := range []string{".nipaignore", "a/.nipa-x", "nipa", "a/b"} {
		require.False(t, isNipaPath(p), p)
	}
}
