package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestSetupSwitchCmd_SwitchesBranch(t *testing.T) {
	root := setupRepoWithTree(t, "main", &serverDomain.TreeNode{Name: "root"})
	content := "dev file content"
	chunks := chunksOf(t, content)

	client := &fakeUpdateClient{
		chunkData: chunkDataMap(t, content),
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "dev.txt",
				Mode:      0o644,
				SizeBytes: int64(len(content)),
				Hash:      chunker.FileHash([]serverDomain.Hash{chunks[0].Hash}),
				Chunks:    chunks,
			}},
		},
	}
	cli := newUpdateCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupSwitchCmd(), "dev")
	require.NoError(t, err)
	require.Contains(t, out, "Switched to branch \"dev\"\n")
	require.Equal(t, "dev", client.branch)

	cfg := loadRepoConfig(t, root)
	require.Equal(t, "dev", cfg.Branch)
	require.Equal(t, "http://example.com/org/project", cfg.Url)
	require.FileExists(t, root+"/dev.txt")
}

func TestSetupSwitchCmd_AlreadyOnBranch(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeUpdateClient{}
	cli := newUpdateCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupSwitchCmd(), "main")
	require.NoError(t, err)
	require.Equal(t, "Already on branch \"main\"\n", out)
	require.Empty(t, client.branch, "no server round-trip is needed when already on the branch")
}

func TestSetupSwitchCmd_NotARepo(t *testing.T) {
	cli := newUpdateCli(t, &fakeUpdateClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupSwitchCmd(), "dev")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupSwitchCmd_MissingArg(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newUpdateCli(t, &fakeUpdateClient{})

	_, err := runCmdInDir(t, root, cli.setupSwitchCmd())
	require.Error(t, err)
}

func TestSetupSwitchCmd_StagedChangesBlocked(t *testing.T) {
	root := setupRepoWithTree(t, "main", &serverDomain.TreeNode{Name: "root"})
	stagePath(t, root, "a.txt")

	cli := newUpdateCli(t, &fakeUpdateClient{manifest: &serverDomain.TreeNode{
		Name:         "root",
		FileChildren: []*serverDomain.File{{Name: "a.txt", Mode: 0o644}},
	}})

	_, err := runCmdInDir(t, root, cli.setupSwitchCmd(), "dev")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot switch branches with staged changes")
}

func loadRepoConfig(t *testing.T, root string) domain.Config {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	cfg, err := lr.LoadConfig()
	require.NoError(t, err)
	return *cfg
}

func TestSetupSwitchCmd_Tag(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeUpdateClient{
		tagInfo:    &domain.Tag{Name: "v1.0.0", CommitID: "abc123"},
		commitTree: &serverDomain.TreeNode{Name: "root"},
	}
	cli := newUpdateCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupSwitchCmd(), "--tag", "v1.0.0")
	require.NoError(t, err)
	require.Contains(t, out, `HEAD detached at tag "v1.0.0"`)
	require.Equal(t, "v1.0.0", client.tagLookup)
	require.Equal(t, "abc123", client.commitTreeID)

	cfg := loadRepoConfig(t, root)
	require.Equal(t, "main", cfg.Branch, "the branch identity must stay configured")
	require.NotNil(t, cfg.Head)
	require.Equal(t, domain.HeadKindTag, cfg.Head.Kind)
	require.Equal(t, "v1.0.0", cfg.Head.Name)
}

func TestSetupSwitchCmd_TagWithBranchRejected(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newUpdateCli(t, &fakeUpdateClient{})

	_, err := runCmdInDir(t, root, cli.setupSwitchCmd(), "--tag", "v1.0.0", "dev")
	require.EqualError(t, err, "--tag cannot be combined with a branch name")
}

func TestSetupSwitchCmd_ReattachDetachedBranch(t *testing.T) {
	root := setupRepo(t, "main")
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{
		Url: "http://example.com/org/project", Branch: "main",
		Head: &domain.HeadRef{Kind: domain.HeadKindTag, Name: "v1.0.0"},
	}))
	require.NoError(t, lr.Close())

	client := &fakeUpdateClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	cli := newUpdateCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupSwitchCmd(), "main")
	require.NoError(t, err)
	require.Contains(t, out, `Switched to branch "main"`)
	require.Equal(t, "main", client.branch, "a detached working copy must re-attach even to the configured branch")

	cfg := loadRepoConfig(t, root)
	require.Nil(t, cfg.Head)
}
