package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

func TestEndToEnd_Blame(t *testing.T) {
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
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	push := func(files map[string]string, msg string) string {
		for p, content := range files {
			writeFile(t, target, p, content)
			stagePath(t, target, p)
		}
		require.NoError(t, pusher.Run(ctx, target, msg))
		id, _ := pinnedCommit(t, target)
		return id
	}

	c1 := push(map[string]string{"top.txt": "a\nb\nc\n", "src/lib/code.txt": "x\ny\n"}, "add files")
	c2 := push(map[string]string{"top.txt": "a\nB\nc\nd\n"}, "change b, add d")
	push(map[string]string{"other.txt": "unrelated\n"}, "unrelated")
	c4 := push(map[string]string{"top.txt": "a\nB\nC\nd\n", "src/lib/code.txt": "x\nY\n"}, "change c and y")

	blame := clientusecase.NewBlame(auth, grpcClient, localrepo.NewLocalRepo())
	owners := func(res *clientusecase.BlameResult) []string {
		out := make([]string, len(res.Lines))
		for i, l := range res.Lines {
			switch {
			case l.Uncommitted:
				out[i] = "new"
			default:
				out[i] = l.CommitID
			}
		}
		return out
	}

	writeFile(t, target, "top.txt", "a\nB\nC\nd\ne\n")
	res, err := blame.Run(ctx, target, "top.txt", "", nil)
	require.NoError(t, err)
	require.Equal(t, []string{c1, c2, c4, c2, "new"}, owners(res))
	require.False(t, res.Truncated)
	require.Len(t, res.Commits, 3)
	require.Equal(t, c4, res.Commits[0].ID)
	require.Equal(t, "change c and y", res.Commits[0].Message)

	res, err = blame.Run(ctx, target, "src/lib/code.txt", "", nil)
	require.NoError(t, err)
	require.Equal(t, []string{c1, c4}, owners(res))

	res, err = blame.Run(ctx, target, "top.txt", c2, nil)
	require.NoError(t, err)
	require.Equal(t, []string{c1, c2, c1, c2}, owners(res))
	require.Equal(t, c2, res.Revision)

	res, err = blame.Run(ctx, target, "top.txt", "main", nil)
	require.NoError(t, err)
	require.Equal(t, []string{c1, c2, c4, c2}, owners(res))

	_, err = blame.Run(ctx, target, "other.txt", c1, nil)
	require.ErrorIs(t, err, clientusecase.ErrFileMissing)
}
