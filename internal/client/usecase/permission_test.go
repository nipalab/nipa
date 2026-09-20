package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubPermissionClient struct {
	connect       func(ctx context.Context, host string) error
	myPermissions func(ctx context.Context, org, project string) (*domain.PermissionInfo, error)
	createRule    func(ctx context.Context, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error)
	listRules     func(ctx context.Context, org, project string) ([]*domain.PBACRuleInfo, error)
	deleteRule    func(ctx context.Context, org, project string, ruleID int64) error
	listPaths     func(ctx context.Context, org, project string) ([]domain.PermissionEntry, error)
	setPath       func(ctx context.Context, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error)
	deletePath    func(ctx context.Context, org, project, pathPrefix string) error
	createGroup   func(ctx context.Context, org, name, description string) (*domain.GroupInfo, error)
	listGroups    func(ctx context.Context, org string) ([]*domain.GroupInfo, error)
	addMember     func(ctx context.Context, org, groupID, userID string) error
	removeMember  func(ctx context.Context, org, groupID, userID string) error
}

func (s *stubPermissionClient) Connect(ctx context.Context, host string) error {
	if s.connect != nil {
		return s.connect(ctx, host)
	}
	return nil
}

func (s *stubPermissionClient) GetMyPermissions(ctx context.Context, org, project string) (*domain.PermissionInfo, error) {
	if s.myPermissions != nil {
		return s.myPermissions(ctx, org, project)
	}
	return nil, nil
}

func (s *stubPermissionClient) CreatePBACRule(ctx context.Context, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error) {
	if s.createRule != nil {
		return s.createRule(ctx, org, project, userID, groupID, pathPrefix, permission)
	}
	return nil, nil
}

func (s *stubPermissionClient) ListPBACRules(ctx context.Context, org, project string) ([]*domain.PBACRuleInfo, error) {
	if s.listRules != nil {
		return s.listRules(ctx, org, project)
	}
	return nil, nil
}

func (s *stubPermissionClient) DeletePBACRule(ctx context.Context, org, project string, ruleID int64) error {
	if s.deleteRule != nil {
		return s.deleteRule(ctx, org, project, ruleID)
	}
	return nil
}

func (s *stubPermissionClient) ListProjectPathPermissions(ctx context.Context, org, project string) ([]domain.PermissionEntry, error) {
	if s.listPaths != nil {
		return s.listPaths(ctx, org, project)
	}
	return nil, nil
}

func (s *stubPermissionClient) SetProjectPathPermission(ctx context.Context, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error) {
	if s.setPath != nil {
		return s.setPath(ctx, org, project, pathPrefix, permission)
	}
	return nil, nil
}

func (s *stubPermissionClient) DeleteProjectPathPermission(ctx context.Context, org, project, pathPrefix string) error {
	if s.deletePath != nil {
		return s.deletePath(ctx, org, project, pathPrefix)
	}
	return nil
}

func (s *stubPermissionClient) CreateGroup(ctx context.Context, org, name, description string) (*domain.GroupInfo, error) {
	if s.createGroup != nil {
		return s.createGroup(ctx, org, name, description)
	}
	return nil, nil
}

func (s *stubPermissionClient) ListGroups(ctx context.Context, org string) ([]*domain.GroupInfo, error) {
	if s.listGroups != nil {
		return s.listGroups(ctx, org)
	}
	return nil, nil
}

func (s *stubPermissionClient) AddGroupMember(ctx context.Context, org, groupID, userID string) error {
	if s.addMember != nil {
		return s.addMember(ctx, org, groupID, userID)
	}
	return nil
}

func (s *stubPermissionClient) RemoveGroupMember(ctx context.Context, org, groupID, userID string) error {
	if s.removeMember != nil {
		return s.removeMember(ctx, org, groupID, userID)
	}
	return nil
}

func newPermissionTestUsecase(t *testing.T, client *stubPermissionClient) *Permission {
	t.Helper()

	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "test-secret")}}
	return NewPermission(NewAuth(&stubRefreshExecutor{}, storage, &stubUserInput{}), client)
}

func TestPermission_My_Success(t *testing.T) {
	var gotHost, gotOrg, gotProject string
	client := &stubPermissionClient{
		connect: func(_ context.Context, host string) error {
			gotHost = host
			return nil
		},
		myPermissions: func(_ context.Context, org, project string) (*domain.PermissionInfo, error) {
			gotOrg, gotProject = org, project
			return &domain.PermissionInfo{ProjectPermission: 7}, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	info, err := p.My(context.Background(), "nipa.example.com", "org", "proj")
	require.NoError(t, err)
	require.Equal(t, uint64(7), info.ProjectPermission)
	require.Equal(t, "nipa.example.com", gotHost)
	require.Equal(t, "org", gotOrg)
	require.Equal(t, "proj", gotProject)
}

func TestPermission_My_ClientError(t *testing.T) {
	wantErr := errors.New("boom")
	client := &stubPermissionClient{
		myPermissions: func(context.Context, string, string) (*domain.PermissionInfo, error) {
			return nil, wantErr
		},
	}
	p := newPermissionTestUsecase(t, client)

	_, err := p.My(context.Background(), "host", "org", "proj")
	require.ErrorIs(t, err, wantErr)
}

func TestPermission_Rules_Success(t *testing.T) {
	want := []*domain.PBACRuleInfo{{ID: 1, PathPrefix: "assets", Permission: 3}}
	client := &stubPermissionClient{
		listRules: func(context.Context, string, string) ([]*domain.PBACRuleInfo, error) {
			return want, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	got, err := p.Rules(context.Background(), "host", "org", "proj")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPermission_Grant_Success(t *testing.T) {
	var gotUserID, gotGroupID, gotPrefix string
	var gotPermission uint64
	client := &stubPermissionClient{
		createRule: func(_ context.Context, _, _, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error) {
			gotUserID, gotGroupID, gotPrefix, gotPermission = userID, groupID, pathPrefix, permission
			return &domain.PBACRuleInfo{ID: 9}, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	rule, err := p.Grant(context.Background(), "host", "org", "proj", "u1", "g1", "assets", 5)
	require.NoError(t, err)
	require.Equal(t, int64(9), rule.ID)
	require.Equal(t, "u1", gotUserID)
	require.Equal(t, "g1", gotGroupID)
	require.Equal(t, "assets", gotPrefix)
	require.Equal(t, uint64(5), gotPermission)
}

func TestPermission_Revoke_Success(t *testing.T) {
	var gotRuleID int64
	client := &stubPermissionClient{
		deleteRule: func(_ context.Context, _, _ string, ruleID int64) error {
			gotRuleID = ruleID
			return nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	require.NoError(t, p.Revoke(context.Background(), "host", "org", "proj", 42))
	require.Equal(t, int64(42), gotRuleID)
}

func TestPermission_PathPermissions_Success(t *testing.T) {
	want := []domain.PermissionEntry{{PathPrefix: "", Permission: 1}}
	client := &stubPermissionClient{
		listPaths: func(context.Context, string, string) ([]domain.PermissionEntry, error) {
			return want, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	got, err := p.PathPermissions(context.Background(), "host", "org", "proj")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPermission_SetPath_Success(t *testing.T) {
	var gotPrefix string
	var gotPermission uint64
	client := &stubPermissionClient{
		setPath: func(_ context.Context, _, _, pathPrefix string, permission uint64) (*domain.PermissionEntry, error) {
			gotPrefix, gotPermission = pathPrefix, permission
			return &domain.PermissionEntry{PathPrefix: pathPrefix, Permission: permission}, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	entry, err := p.SetPath(context.Background(), "host", "org", "proj", "assets", 3)
	require.NoError(t, err)
	require.Equal(t, "assets", entry.PathPrefix)
	require.Equal(t, uint64(3), entry.Permission)
	require.Equal(t, "assets", gotPrefix)
	require.Equal(t, uint64(3), gotPermission)
}

func TestPermission_RemovePath_Success(t *testing.T) {
	var gotPrefix string
	client := &stubPermissionClient{
		deletePath: func(_ context.Context, _, _, pathPrefix string) error {
			gotPrefix = pathPrefix
			return nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	require.NoError(t, p.RemovePath(context.Background(), "host", "org", "proj", "assets"))
	require.Equal(t, "assets", gotPrefix)
}

func TestPermission_CreateGroup_Success(t *testing.T) {
	var gotOrg, gotName, gotDescription string
	client := &stubPermissionClient{
		createGroup: func(_ context.Context, org, name, description string) (*domain.GroupInfo, error) {
			gotOrg, gotName, gotDescription = org, name, description
			return &domain.GroupInfo{ID: "g1", Name: name}, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	group, err := p.CreateGroup(context.Background(), "host", "org", "artists", "2d team")
	require.NoError(t, err)
	require.Equal(t, "artists", group.Name)
	require.Equal(t, "org", gotOrg)
	require.Equal(t, "artists", gotName)
	require.Equal(t, "2d team", gotDescription)
}

func TestPermission_Groups_Success(t *testing.T) {
	want := []*domain.GroupInfo{{ID: "g1", Name: "artists"}}
	client := &stubPermissionClient{
		listGroups: func(context.Context, string) ([]*domain.GroupInfo, error) {
			return want, nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	got, err := p.Groups(context.Background(), "host", "org")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPermission_AddGroupMember_Success(t *testing.T) {
	var gotOrg, gotGroupID, gotUserID string
	client := &stubPermissionClient{
		addMember: func(_ context.Context, org, groupID, userID string) error {
			gotOrg, gotGroupID, gotUserID = org, groupID, userID
			return nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	require.NoError(t, p.AddGroupMember(context.Background(), "host", "org", "g1", "u1"))
	require.Equal(t, "org", gotOrg)
	require.Equal(t, "g1", gotGroupID)
	require.Equal(t, "u1", gotUserID)
}

func TestPermission_RemoveGroupMember_Success(t *testing.T) {
	var gotGroupID, gotUserID string
	client := &stubPermissionClient{
		removeMember: func(_ context.Context, _, groupID, userID string) error {
			gotGroupID, gotUserID = groupID, userID
			return nil
		},
	}
	p := newPermissionTestUsecase(t, client)

	require.NoError(t, p.RemoveGroupMember(context.Background(), "host", "org", "g1", "u1"))
	require.Equal(t, "g1", gotGroupID)
	require.Equal(t, "u1", gotUserID)
}

func TestPermission_ConnectError(t *testing.T) {
	wantErr := errors.New("dial failed")
	client := &stubPermissionClient{
		connect: func(context.Context, string) error { return wantErr },
	}
	p := newPermissionTestUsecase(t, client)
	ctx := context.Background()

	_, err := p.My(ctx, "host", "org", "proj")
	require.ErrorIs(t, err, wantErr)
	_, err = p.Rules(ctx, "host", "org", "proj")
	require.ErrorIs(t, err, wantErr)
	_, err = p.Grant(ctx, "host", "org", "proj", "u1", "", "", 1)
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, p.Revoke(ctx, "host", "org", "proj", 1), wantErr)
	_, err = p.PathPermissions(ctx, "host", "org", "proj")
	require.ErrorIs(t, err, wantErr)
	_, err = p.SetPath(ctx, "host", "org", "proj", "", 1)
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, p.RemovePath(ctx, "host", "org", "proj", ""), wantErr)
	_, err = p.CreateGroup(ctx, "host", "org", "g", "")
	require.ErrorIs(t, err, wantErr)
	_, err = p.Groups(ctx, "host", "org")
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, p.AddGroupMember(ctx, "host", "org", "g1", "u1"), wantErr)
	require.ErrorIs(t, p.RemoveGroupMember(ctx, "host", "org", "g1", "u1"), wantErr)
}

func TestPermission_NotLoggedIn(t *testing.T) {
	wantErr := errors.New("input closed")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "not-a-jwt"}}
	p := NewPermission(NewAuth(&stubRefreshExecutor{}, storage, &stubUserInput{err: wantErr}), &stubPermissionClient{})

	_, err := p.My(context.Background(), "host", "org", "proj")
	require.ErrorIs(t, err, wantErr)
}
