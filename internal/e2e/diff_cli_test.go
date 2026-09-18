package e2e

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/cli"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

// runNipaCLI drives the real CLI in-process against dir, capturing stdout
// and stderr. Cobra builds a fresh command tree per Run, so flags never
// leak between invocations.
func runNipaCLI(t *testing.T, c *cli.Cli, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	oldArgs, oldWd, oldOut, oldErr := os.Args, mustGetwd(t), os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	os.Args = append([]string{"nipa"}, args...)
	os.Stdout, os.Stderr = outW, errW
	require.NoError(t, os.Chdir(dir))
	runErr := c.Run()
	_ = outW.Close()
	_ = errW.Close()
	os.Args, os.Stdout, os.Stderr = oldArgs, oldOut, oldErr
	require.NoError(t, os.Chdir(oldWd))
	out, _ := io.ReadAll(outR)
	errOut, _ := io.ReadAll(errR)
	return string(out), string(errOut), runErr
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}

func TestEndToEnd_DiffCLI(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})

	require.NoError(t, grpcClient.Connect(ctx, host))
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	writeFile(t, target, "a.txt", "v1\n")
	stagePath(t, target, "a.txt")
	require.NoError(t, pusher.Run(ctx, target, "first"))

	writeFile(t, target, "a.txt", "v2\n")
	writeFile(t, target, "b.txt", "new\n")
	stagePath(t, target, "a.txt")
	stagePath(t, target, "b.txt")
	require.NoError(t, pusher.Run(ctx, target, "second"))

	entries, err := repo.Log(ctx, host, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	hash1 := entries[1].Hash.String()
	hash2 := entries[0].Hash.String()

	reg := &e2eCliRegistry{
		auth:   auth,
		repo:   repo,
		push:   pusher,
		update: updater,
		merge:  clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher),
		diff:   clientusecase.NewDiff(auth, grpcClient, localrepo.NewLocalRepo()),
	}
	c := cli.NewCli(reg, grpcClient)

	// clean tree: empty output
	out, _, err := runNipaCLI(t, c, target, "diff", "--no-pager")
	require.NoError(t, err)
	require.Empty(t, out)

	// pushed content was uploaded, never downloaded: the old side is
	// unavailable until an update warms the cache
	writeFile(t, target, "a.txt", "v3-local\n")
	out, _, err = runNipaCLI(t, c, target, "diff", "--no-pager")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\ncontent not available locally (run `nipa update` to fetch it)\n", out)

	// update does not clobber the uncommitted edit, but it downloads the
	// missing chunks, warming the cache so the old side renders
	require.NoError(t, updater.Run(ctx, target))
	out, _, err = runNipaCLI(t, c, target, "diff", "--no-pager")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-v2\n+v3-local\n", out)

	// output modes on the same state
	out, _, err = runNipaCLI(t, c, target, "diff", "--stat")
	require.NoError(t, err)
	require.Equal(t, " a.txt | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n", out)

	out, _, err = runNipaCLI(t, c, target, "diff", "--name-only")
	require.NoError(t, err)
	require.Equal(t, "a.txt\n", out)

	out, _, err = runNipaCLI(t, c, target, "diff", "--name-status")
	require.NoError(t, err)
	require.Equal(t, "M\ta.txt\n", out)

	// revision vs working copy downloads the old side
	out, _, err = runNipaCLI(t, c, target, "diff", hash1, "--no-pager")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-v1\n+v3-local\n", out)

	// revision vs revision
	out, _, err = runNipaCLI(t, c, target, "diff", hash1, hash2, "--no-pager")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-v1\n+v2\n"+
		"diff --nipa a/b.txt b/b.txt\nnew file mode 2\n--- /dev/null\n+++ b/b.txt\n@@ -0,0 +1,1 @@\n+new\n", out)

	// branch tip equals the second commit
	out, _, err = runNipaCLI(t, c, target, "diff", "main", hash2, "--no-pager")
	require.NoError(t, err)
	require.Empty(t, out)

	// unknown revision
	_, _, err = runNipaCLI(t, c, target, "diff", "nope!!!")
	require.ErrorContains(t, err, `revision "nope!!!" not found`)

	// deleted file alongside the modification
	require.NoError(t, os.Remove(filepath.Join(target, "b.txt")))
	out, _, err = runNipaCLI(t, c, target, "diff", "--name-status")
	require.NoError(t, err)
	require.Equal(t, "M\ta.txt\nD\tb.txt\n", out)

	// staged binary renders without content lines
	img := "PNG\x00img"
	writeFile(t, target, "img.bin", img)
	stagePath(t, target, "img.bin")
	out, _, err = runNipaCLI(t, c, target, "diff", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "Binary files a/img.bin and b/img.bin differ\n")
	out, _, err = runNipaCLI(t, c, target, "diff", "--stat")
	require.NoError(t, err)
	require.Contains(t, out, " img.bin | Bin 0 -> 7 bytes\n")
}
