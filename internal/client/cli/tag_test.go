package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeTagClient struct {
	connectHost string
	connectErr  error

	createName       string
	createMessage    string
	createBranch     string
	createCommitID   string
	createCommitHash string
	createResult     *domain.Tag
	createErr        error

	listResult []*domain.Tag
	listErr    error

	deleteName string
	deleteErr  error
}

func (f *fakeTagClient) Connect(_ context.Context, host string) error {
	f.connectHost = host
	return f.connectErr
}

func (f *fakeTagClient) CreateTag(_ context.Context, _, _, name, message, fromBranch, fromCommitID, fromCommitHash string) (*domain.Tag, error) {
	f.createName, f.createMessage = name, message
	f.createBranch, f.createCommitID, f.createCommitHash = fromBranch, fromCommitID, fromCommitHash
	if f.createResult != nil || f.createErr != nil {
		return f.createResult, f.createErr
	}
	return &domain.Tag{Name: name, CommitID: fromCommitID}, nil
}

func (f *fakeTagClient) ListTags(_ context.Context, _, _ string) ([]*domain.Tag, error) {
	return f.listResult, f.listErr
}

func (f *fakeTagClient) DeleteTag(_ context.Context, _, _, name string) error {
	f.deleteName = name
	return f.deleteErr
}

func newTagCli(t *testing.T, client *fakeTagClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{tag: usecase.NewTag(auth, client, localrepo.NewLocalRepo())}, &fakeConnector{})
}

func setupRepoWithCommit(t *testing.T, branch, commitID, commitHash string) string {
	t.Helper()
	root := setupRepo(t, branch)
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveCommit(commitID, commitHash))
	require.NoError(t, lr.Close())
	return root
}

func TestSetupTagCmd_List_Rows(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeTagClient{listResult: []*domain.Tag{
		{Name: "v1.0.0", CommitID: "abc123", Message: "first release"},
		{Name: "v1.0.1", CommitID: "def456"},
	}}
	cli := newTagCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupTagCmd())
	require.NoError(t, err)
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "v1.0.0")
	require.Contains(t, out, "first release")
	require.Contains(t, out, "def456")
}

func TestSetupTagCmd_List_Empty(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	out, err := runCmdInDir(t, root, cli.setupTagCmd())
	require.NoError(t, err)
	require.Contains(t, out, "no tags")
}

func TestSetupTagCmd_List_JSON(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeTagClient{listResult: []*domain.Tag{{Name: "v1.0.0", CommitID: "abc123"}}}
	cli := newTagCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupTagCmd(), "--json")
	require.NoError(t, err)
	var tags output.Tags
	require.NoError(t, json.Unmarshal([]byte(out), &tags))
	require.Len(t, tags.Tags, 1)
	require.Equal(t, "v1.0.0", tags.Tags[0].Name)
	require.Equal(t, "abc123", tags.Tags[0].CommitID)
}

func TestSetupTagCmd_Create_DefaultsToPinnedCommit(t *testing.T) {
	root := setupRepoWithCommit(t, "main", "abc123", "deadbeef")
	client := &fakeTagClient{createResult: &domain.Tag{Name: "v1.0.0", CommitID: "abc123"}}
	cli := newTagCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupTagCmd(), "-c", "v1.0.0", "-m", "release")
	require.NoError(t, err)
	require.Contains(t, out, `Created tag "v1.0.0" at commit abc123.`)
	require.Equal(t, "v1.0.0", client.createName)
	require.Equal(t, "release", client.createMessage)
	require.Empty(t, client.createBranch)
	require.Equal(t, "abc123", client.createCommitID)
	require.Equal(t, "deadbeef", client.createCommitHash)
}

func TestSetupTagCmd_Create_BranchFlag(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeTagClient{createResult: &domain.Tag{Name: "v2.0.0", CommitID: "abc123"}}
	cli := newTagCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-c", "v2.0.0", "--branch", "release")
	require.NoError(t, err)
	require.Equal(t, "release", client.createBranch)
	require.Empty(t, client.createCommitID)
}

func TestSetupTagCmd_Create_CommitFlag(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeTagClient{createResult: &domain.Tag{Name: "v3.0.0", CommitID: "abc123"}}
	cli := newTagCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-c", "v3.0.0", "--commit", "abc123")
	require.NoError(t, err)
	require.Equal(t, "abc123", client.createCommitID)
	require.Empty(t, client.createCommitHash)
}

func TestSetupTagCmd_Create_NoName(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-c")
	require.EqualError(t, err, "a tag name is required: nipa tag -c <name>")
}

func TestSetupTagCmd_Create_Error(t *testing.T) {
	root := setupRepo(t, "main")
	wantErr := &domain.Error{Code: 409, Message: `tag "v1.0.0" already exists`}
	cli := newTagCli(t, &fakeTagClient{createErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-c", "v1.0.0", "--branch", "main")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupTagCmd_Delete(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeTagClient{}
	cli := newTagCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupTagCmd(), "-d", "v1.0.0")
	require.NoError(t, err)
	require.Contains(t, out, `Deleted tag "v1.0.0".`)
	require.Equal(t, "v1.0.0", client.deleteName)
}

func TestSetupTagCmd_Delete_NoName(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-d")
	require.EqualError(t, err, "a tag name is required: nipa tag -d <name>")
}

func TestSetupTagCmd_Delete_Error(t *testing.T) {
	root := setupRepo(t, "main")
	wantErr := errors.New("boom")
	cli := newTagCli(t, &fakeTagClient{deleteErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-d", "v1.0.0")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupTagCmd_FlagsMutuallyExclusive(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "-c", "v1.0.0", "-d")
	require.EqualError(t, err, "--create and --delete are mutually exclusive")
}

func TestSetupTagCmd_TargetFlagsRequireCreate(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "--branch", "main")
	require.EqualError(t, err, "--message, --branch and --commit require --create")
}

func TestSetupTagCmd_JSONWithCreate(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "--json", "-c", "v1.0.0")
	require.EqualError(t, err, "--json cannot be combined with --create or --delete")
}

func TestSetupTagCmd_UnknownArg(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, root, cli.setupTagCmd(), "v1.0.0")
	require.EqualError(t, err, "to create a tag use nipa tag -c <name>; to delete one use nipa tag -d <name>")
}

func TestSetupTagCmd_NotARepo(t *testing.T) {
	cli := newTagCli(t, &fakeTagClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupTagCmd())
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}
