package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakePermissionClient struct {
	connect      func(ctx context.Context, host string) error
	myInfo       func(ctx context.Context, org, project string) (*domain.PermissionInfo, error)
	createRule   func(ctx context.Context, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error)
	listRules    func(ctx context.Context, org, project string) ([]*domain.PBACRuleInfo, error)
	deleteRule   func(ctx context.Context, org, project string, ruleID int64) error
	listPaths    func(ctx context.Context, org, project string) ([]domain.PermissionEntry, error)
	setPath      func(ctx context.Context, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error)
	deletePath   func(ctx context.Context, org, project, pathPrefix string) error
	createGroup  func(ctx context.Context, org, name, description string) (*domain.GroupInfo, error)
	listGroups   func(ctx context.Context, org string) ([]*domain.GroupInfo, error)
	addMember    func(ctx context.Context, org, groupID, userID string) error
	removeMember func(ctx context.Context, org, groupID, userID string) error
}

func (f *fakePermissionClient) Connect(ctx context.Context, host string) error {
	if f.connect != nil {
		return f.connect(ctx, host)
	}
	return nil
}

func (f *fakePermissionClient) GetMyPermissions(ctx context.Context, org, project string) (*domain.PermissionInfo, error) {
	if f.myInfo != nil {
		return f.myInfo(ctx, org, project)
	}
	return nil, nil
}

func (f *fakePermissionClient) CreatePBACRule(ctx context.Context, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error) {
	if f.createRule != nil {
		return f.createRule(ctx, org, project, userID, groupID, pathPrefix, permission)
	}
	return nil, nil
}

func (f *fakePermissionClient) ListPBACRules(ctx context.Context, org, project string) ([]*domain.PBACRuleInfo, error) {
	if f.listRules != nil {
		return f.listRules(ctx, org, project)
	}
	return nil, nil
}

func (f *fakePermissionClient) DeletePBACRule(ctx context.Context, org, project string, ruleID int64) error {
	if f.deleteRule != nil {
		return f.deleteRule(ctx, org, project, ruleID)
	}
	return nil
}

func (f *fakePermissionClient) ListProjectPathPermissions(ctx context.Context, org, project string) ([]domain.PermissionEntry, error) {
	if f.listPaths != nil {
		return f.listPaths(ctx, org, project)
	}
	return nil, nil
}

func (f *fakePermissionClient) SetProjectPathPermission(ctx context.Context, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error) {
	if f.setPath != nil {
		return f.setPath(ctx, org, project, pathPrefix, permission)
	}
	return nil, nil
}

func (f *fakePermissionClient) DeleteProjectPathPermission(ctx context.Context, org, project, pathPrefix string) error {
	if f.deletePath != nil {
		return f.deletePath(ctx, org, project, pathPrefix)
	}
	return nil
}

func (f *fakePermissionClient) CreateGroup(ctx context.Context, org, name, description string) (*domain.GroupInfo, error) {
	if f.createGroup != nil {
		return f.createGroup(ctx, org, name, description)
	}
	return nil, nil
}

func (f *fakePermissionClient) ListGroups(ctx context.Context, org string) ([]*domain.GroupInfo, error) {
	if f.listGroups != nil {
		return f.listGroups(ctx, org)
	}
	return nil, nil
}

func (f *fakePermissionClient) AddGroupMember(ctx context.Context, org, groupID, userID string) error {
	if f.addMember != nil {
		return f.addMember(ctx, org, groupID, userID)
	}
	return nil
}

func (f *fakePermissionClient) RemoveGroupMember(ctx context.Context, org, groupID, userID string) error {
	if f.removeMember != nil {
		return f.removeMember(ctx, org, groupID, userID)
	}
	return nil
}

func newPermissionCli(t *testing.T, client *fakePermissionClient) *Cli {
	t.Helper()

	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{permission: usecase.NewPermission(auth, client)}, &fakeConnector{})
}

func setupRepoWithURL(t *testing.T, url string) string {
	t.Helper()

	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	require.NoError(t, lr.SaveConfig(domain.Config{Url: url, Branch: "main"}))
	return root
}

func TestSetupAclListCmd_NoRules(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "no access rules")
}

func TestSetupAclListCmd_Rules(t *testing.T) {
	var gotOrg, gotProject string
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		connect: func(context.Context, string) error { return nil },
		listRules: func(_ context.Context, org, project string) ([]*domain.PBACRuleInfo, error) {
			gotOrg, gotProject = org, project
			return []*domain.PBACRuleInfo{
				{ID: 5, UserID: "u1", PathPrefix: "", Permission: permissionRead},
				{ID: 6, GroupID: "g1", PathPrefix: "assets", Permission: permissionRead | permissionWrite},
			}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "5\tuser:u1\t/\tread")
	require.Contains(t, out, "6\tgroup:g1\tassets\tread,write")
	require.Equal(t, "org", gotOrg)
	require.Equal(t, "project", gotProject)
}

func TestSetupAclListCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		listRules: func(context.Context, string, string) ([]*domain.PBACRuleInfo, error) {
			return nil, wantErr
		},
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "list")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupAclGrantCmd_Success(t *testing.T) {
	var gotUser, gotGroup, gotPath string
	var gotPermission uint64
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		createRule: func(_ context.Context, _, _, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error) {
			gotUser, gotGroup, gotPath, gotPermission = userID, groupID, pathPrefix, permission
			return &domain.PBACRuleInfo{ID: 9, UserID: userID, PathPrefix: pathPrefix, Permission: permission}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(),
		"grant", "--user", "u1", "--path", "assets", "--permission", "read,write")
	require.NoError(t, err)
	require.Contains(t, out, `Granted rule 9 (read,write on "assets")`)
	require.Equal(t, "u1", gotUser)
	require.Empty(t, gotGroup)
	require.Equal(t, "assets", gotPath)
	require.Equal(t, permissionRead|permissionWrite, gotPermission)
}

func TestSetupAclGrantCmd_SubjectRequired(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "grant", "--permission", "read")
	require.EqualError(t, err, "exactly one of --user or --group is required")

	_, err = runCmdInDir(t, root, cli.setupAclCmd(),
		"grant", "--user", "u1", "--group", "g1", "--permission", "read")
	require.EqualError(t, err, "exactly one of --user or --group is required")
}

func TestSetupAclGrantCmd_InvalidPermission(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "grant", "--group", "g1", "--permission", "execute")
	require.Error(t, err)
}

func TestSetupAclGrantCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		createRule: func(context.Context, string, string, string, string, string, uint64) (*domain.PBACRuleInfo, error) {
			return nil, wantErr
		},
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "grant", "--user", "u1", "--permission", "read")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupAclRevokeCmd_Success(t *testing.T) {
	var gotID int64
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		deleteRule: func(_ context.Context, _, _ string, ruleID int64) error {
			gotID = ruleID
			return nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "revoke", "--id", "5")
	require.NoError(t, err)
	require.Contains(t, out, "Deleted rule 5")
	require.Equal(t, int64(5), gotID)
}

func TestSetupAclRevokeCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		deleteRule: func(context.Context, string, string, int64) error { return wantErr },
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "revoke", "--id", "5")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupAclMyCmd_Success(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		myInfo: func(context.Context, string, string) (*domain.PermissionInfo, error) {
			return &domain.PermissionInfo{
				ProjectPermission: permissionRead | permissionWrite,
				Rules:             []domain.PermissionEntry{{PathPrefix: "", Permission: permissionRead}},
				Defaults:          []domain.PermissionEntry{{PathPrefix: "docs", Permission: permissionLock}},
			}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "my")
	require.NoError(t, err)
	require.Contains(t, out, "project: read,write")
	require.Contains(t, out, "rule\t/\tread")
	require.Contains(t, out, "default\tdocs\tlock")
}

func TestSetupAclMyCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		myInfo: func(context.Context, string, string) (*domain.PermissionInfo, error) {
			return nil, wantErr
		},
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "my")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupPermissionListCmd_Empty(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "permission", "list")
	require.NoError(t, err)
	require.Contains(t, out, "no path defaults")
}

func TestSetupPermissionListCmd_Entries(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		listPaths: func(context.Context, string, string) ([]domain.PermissionEntry, error) {
			return []domain.PermissionEntry{
				{PathPrefix: "", Permission: permissionRead},
				{PathPrefix: "assets", Permission: permissionRead | permissionWrite | permissionAdmin},
			}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "permission", "list")
	require.NoError(t, err)
	require.Contains(t, out, "/\tread")
	require.Contains(t, out, "assets\tread,write,admin")
}

func TestSetupPermissionListCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		listPaths: func(context.Context, string, string) ([]domain.PermissionEntry, error) {
			return nil, wantErr
		},
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "permission", "list")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupPermissionSetCmd_Success(t *testing.T) {
	var gotPath string
	var gotPermission uint64
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		setPath: func(_ context.Context, _, _, pathPrefix string, permission uint64) (*domain.PermissionEntry, error) {
			gotPath, gotPermission = pathPrefix, permission
			return &domain.PermissionEntry{PathPrefix: pathPrefix, Permission: permission}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(),
		"permission", "set", "--path", "docs", "--permission", "lock")
	require.NoError(t, err)
	require.Contains(t, out, "Set lock default on docs")
	require.Equal(t, "docs", gotPath)
	require.Equal(t, permissionLock, gotPermission)
}

func TestSetupPermissionSetCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		setPath: func(context.Context, string, string, string, uint64) (*domain.PermissionEntry, error) {
			return nil, wantErr
		},
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(),
		"permission", "set", "--path", "docs", "--permission", "read")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupPermissionRemoveCmd_Success(t *testing.T) {
	var gotPath string
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		deletePath: func(_ context.Context, _, _, pathPrefix string) error {
			gotPath = pathPrefix
			return nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupAclCmd(), "permission", "remove", "--path", "docs")
	require.NoError(t, err)
	require.Contains(t, out, "Removed default on docs")
	require.Equal(t, "docs", gotPath)

	out, err = runCmdInDir(t, root, cli.setupAclCmd(), "permission", "remove", "--path", "")
	require.NoError(t, err)
	require.Contains(t, out, "Removed default on /")
}

func TestSetupPermissionRemoveCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		deletePath: func(context.Context, string, string, string) error { return wantErr },
	})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "permission", "remove", "--path", "docs")
	require.ErrorIs(t, err, wantErr)
}

func TestAcl_NotARepo(t *testing.T) {
	cli := newPermissionCli(t, &fakePermissionClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupAclCmd(), "list")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}

func TestAcl_InvalidURLConfig(t *testing.T) {
	root := setupRepoWithURL(t, "http://example.com/onlyone")
	cli := newPermissionCli(t, &fakePermissionClient{})

	_, err := runCmdInDir(t, root, cli.setupAclCmd(), "list")
	require.Error(t, err)
}

func TestSetupGroupCreateCmd_Success(t *testing.T) {
	var gotOrg, gotName, gotDescription string
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		createGroup: func(_ context.Context, org, name, description string) (*domain.GroupInfo, error) {
			gotOrg, gotName, gotDescription = org, name, description
			return &domain.GroupInfo{ID: "g1", Name: name}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupGroupCmd(), "create", "artists", "--description", "2d team")
	require.NoError(t, err)
	require.Contains(t, out, "Created group artists (g1)")
	require.Equal(t, "org", gotOrg)
	require.Equal(t, "artists", gotName)
	require.Equal(t, "2d team", gotDescription)
}

func TestSetupGroupCreateCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		createGroup: func(context.Context, string, string, string) (*domain.GroupInfo, error) {
			return nil, wantErr
		},
	})

	_, err := runCmdInDir(t, root, cli.setupGroupCmd(), "create", "artists")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupGroupListCmd_Empty(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{})

	out, err := runCmdInDir(t, root, cli.setupGroupCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "no groups")
}

func TestSetupGroupListCmd_Groups(t *testing.T) {
	var gotOrg string
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		listGroups: func(_ context.Context, org string) ([]*domain.GroupInfo, error) {
			gotOrg = org
			return []*domain.GroupInfo{
				{ID: "g1", OrgID: "1", Name: "artists", Description: "2d team"},
				{ID: "g2", OrgID: "1", Name: "engineers"},
			}, nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupGroupCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "g1\tartists\t2d team")
	require.Contains(t, out, "g2\tengineers\t")
	require.Equal(t, "org", gotOrg)
}

func TestSetupGroupListCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		listGroups: func(context.Context, string) ([]*domain.GroupInfo, error) { return nil, wantErr },
	})

	_, err := runCmdInDir(t, root, cli.setupGroupCmd(), "list")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupGroupAddMemberCmd_Success(t *testing.T) {
	var gotGroup, gotUser string
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		addMember: func(_ context.Context, _, groupID, userID string) error {
			gotGroup, gotUser = groupID, userID
			return nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupGroupCmd(), "add-member", "--group", "g1", "--user", "u1")
	require.NoError(t, err)
	require.Contains(t, out, "Added user u1 to group g1")
	require.Equal(t, "g1", gotGroup)
	require.Equal(t, "u1", gotUser)
}

func TestSetupGroupAddMemberCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		addMember: func(context.Context, string, string, string) error { return wantErr },
	})

	_, err := runCmdInDir(t, root, cli.setupGroupCmd(), "add-member", "--group", "g1", "--user", "u1")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupGroupRemoveMemberCmd_Success(t *testing.T) {
	var gotGroup, gotUser string
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		removeMember: func(_ context.Context, _, groupID, userID string) error {
			gotGroup, gotUser = groupID, userID
			return nil
		},
	})

	out, err := runCmdInDir(t, root, cli.setupGroupCmd(), "remove-member", "--group", "g1", "--user", "u1")
	require.NoError(t, err)
	require.Contains(t, out, "Removed user u1 from group g1")
	require.Equal(t, "g1", gotGroup)
	require.Equal(t, "u1", gotUser)
}

func TestSetupGroupRemoveMemberCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "main")
	cli := newPermissionCli(t, &fakePermissionClient{
		removeMember: func(context.Context, string, string, string) error { return wantErr },
	})

	_, err := runCmdInDir(t, root, cli.setupGroupCmd(), "remove-member", "--group", "g1", "--user", "u1")
	require.ErrorIs(t, err, wantErr)
}

func TestGroup_NotARepo(t *testing.T) {
	cli := newPermissionCli(t, &fakePermissionClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupGroupCmd(), "list")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}
