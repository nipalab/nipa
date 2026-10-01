package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubTagClient struct {
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

func (s *stubTagClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubTagClient) CreateTag(_ context.Context, _, _, name, message, fromBranch, fromCommitID, fromCommitHash string) (*domain.Tag, error) {
	s.createName, s.createMessage = name, message
	s.createBranch, s.createCommitID, s.createCommitHash = fromBranch, fromCommitID, fromCommitHash
	return s.createResult, s.createErr
}

func (s *stubTagClient) ListTags(_ context.Context, _, _ string) ([]*domain.Tag, error) {
	return s.listResult, s.listErr
}

func (s *stubTagClient) DeleteTag(_ context.Context, _, _, name string) error {
	s.deleteName = name
	return s.deleteErr
}

func newTestTagUsecase(t *testing.T, local tagLocalRepo, client tagClient) *Tag {
	t.Helper()
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	return NewTag(auth, client, local)
}

func tagTestLocalRepo() *stubLocalRepo {
	return &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		loadCommit: &domain.LocalCommit{CommitID: "abc123", CommitHash: "deadbeef"},
	}
}

func TestTagUsecase_Create_DefaultsToPinnedCommit(t *testing.T) {
	local := tagTestLocalRepo()
	client := &stubTagClient{createResult: &domain.Tag{Name: "v1.0.0", CommitID: "abc123"}}

	tag, err := newTestTagUsecase(t, local, client).
		Create(context.Background(), t.TempDir(), " v1.0.0 ", "release", TagTarget{})
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", tag.Name)
	require.Equal(t, "example.com", client.connectHost)
	require.Equal(t, "v1.0.0", client.createName)
	require.Equal(t, "release", client.createMessage)
	require.Empty(t, client.createBranch)
	require.Equal(t, "abc123", client.createCommitID)
	require.Equal(t, "deadbeef", client.createCommitHash)
}

func TestTagUsecase_Create_FromBranch(t *testing.T) {
	client := &stubTagClient{createResult: &domain.Tag{Name: "v2.0.0"}}

	_, err := newTestTagUsecase(t, tagTestLocalRepo(), client).
		Create(context.Background(), t.TempDir(), "v2.0.0", "", TagTarget{Branch: "release"})
	require.NoError(t, err)
	require.Equal(t, "release", client.createBranch)
	require.Empty(t, client.createCommitID)
	require.Empty(t, client.createCommitHash)
}

func TestTagUsecase_Create_FromCommitID(t *testing.T) {
	client := &stubTagClient{createResult: &domain.Tag{Name: "v3.0.0"}}

	_, err := newTestTagUsecase(t, tagTestLocalRepo(), client).
		Create(context.Background(), t.TempDir(), "v3.0.0", "", TagTarget{Commit: "abc123"})
	require.NoError(t, err)
	require.Equal(t, "abc123", client.createCommitID)
	require.Empty(t, client.createBranch)
	require.Empty(t, client.createCommitHash)
}

func TestTagUsecase_Create_FromCommitHash(t *testing.T) {
	client := &stubTagClient{createResult: &domain.Tag{Name: "v4.0.0"}}
	hash := strings.Repeat("ab", 32)

	_, err := newTestTagUsecase(t, tagTestLocalRepo(), client).
		Create(context.Background(), t.TempDir(), "v4.0.0", "", TagTarget{Commit: hash})
	require.NoError(t, err)
	require.Equal(t, hash, client.createCommitHash)
	require.Empty(t, client.createCommitID)
	require.Empty(t, client.createBranch)
}

func TestTagUsecase_Create_InvalidCommit(t *testing.T) {
	client := &stubTagClient{}

	_, err := newTestTagUsecase(t, tagTestLocalRepo(), client).
		Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{Commit: "not a ref!"})
	require.EqualError(t, err, `invalid commit "not a ref!": expected a base36 commit id or hex hash`)
	require.Empty(t, client.createName)
}

func TestTagUsecase_Create_Validation(t *testing.T) {
	client := &stubTagClient{}
	uc := newTestTagUsecase(t, tagTestLocalRepo(), client)

	_, err := uc.Create(context.Background(), t.TempDir(), "  ", "", TagTarget{})
	require.EqualError(t, err, "tag name is required")

	_, err = uc.Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{Branch: "main", Commit: "abc123"})
	require.EqualError(t, err, "--branch and --commit are mutually exclusive")
	require.Empty(t, client.createName)
}

func TestTagUsecase_Create_NoPinnedCommit(t *testing.T) {
	local := tagTestLocalRepo()
	local.loadCommit = nil

	_, err := newTestTagUsecase(t, local, &stubTagClient{}).
		Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{})
	require.EqualError(t, err, "no commit checked out; run nipa update first, or pass --branch/--commit")
}

func TestTagUsecase_Create_LoadCommitError(t *testing.T) {
	wantErr := errors.New("no pin")
	local := tagTestLocalRepo()
	local.loadCommitErr = wantErr

	_, err := newTestTagUsecase(t, local, &stubTagClient{}).
		Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{})
	require.ErrorIs(t, err, wantErr)
}

func TestTagUsecase_Create_ConnectError(t *testing.T) {
	wantErr := errors.New("dial failed")
	client := &stubTagClient{connectErr: wantErr}

	_, err := newTestTagUsecase(t, tagTestLocalRepo(), client).
		Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{})
	require.ErrorIs(t, err, wantErr)
}

func TestTagUsecase_Create_ConfigError(t *testing.T) {
	wantErr := errors.New("missing config")
	local := &stubLocalRepo{configLoadErr: wantErr}

	_, err := newTestTagUsecase(t, local, &stubTagClient{}).
		Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{})
	require.ErrorIs(t, err, wantErr)
}

func TestTagUsecase_Create_ClientError(t *testing.T) {
	wantErr := &domain.Error{Code: 409, Message: `tag "v1.0.0" already exists`}
	client := &stubTagClient{createErr: wantErr}

	_, err := newTestTagUsecase(t, tagTestLocalRepo(), client).
		Create(context.Background(), t.TempDir(), "v1.0.0", "", TagTarget{})
	require.ErrorIs(t, err, wantErr)
}

func TestTagUsecase_List(t *testing.T) {
	client := &stubTagClient{listResult: []*domain.Tag{{Name: "v1.0.0"}}}

	tags, err := newTestTagUsecase(t, tagTestLocalRepo(), client).List(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.Len(t, tags, 1)

	wantErr := errors.New("boom")
	_, err = newTestTagUsecase(t, tagTestLocalRepo(), &stubTagClient{listErr: wantErr}).
		List(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestTagUsecase_Delete(t *testing.T) {
	client := &stubTagClient{}
	require.NoError(t, newTestTagUsecase(t, tagTestLocalRepo(), client).
		Delete(context.Background(), t.TempDir(), " v1.0.0 "))
	require.Equal(t, "v1.0.0", client.deleteName)

	err := newTestTagUsecase(t, tagTestLocalRepo(), &stubTagClient{}).
		Delete(context.Background(), t.TempDir(), "  ")
	require.EqualError(t, err, "tag name is required")

	wantErr := &domain.Error{Code: 404, Message: `tag "missing" not found`}
	err = newTestTagUsecase(t, tagTestLocalRepo(), &stubTagClient{deleteErr: wantErr}).
		Delete(context.Background(), t.TempDir(), "missing")
	require.ErrorIs(t, err, wantErr)
}
