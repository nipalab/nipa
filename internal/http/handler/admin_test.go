package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

func (env *handlerTestEnv) createUser(t *testing.T, name, email string) *domain.User {
	t.Helper()

	user, err := env.userRepo.Create(context.Background(), domain.User{
		ID:       env.node.Generate(),
		Name:     name,
		Email:    email,
		Password: "hashed-password",
	})
	require.NoError(t, err)
	return user
}

func adminAppCtx(userID snow.ID, pathParams map[string]string, body string) *fakeAppContext {
	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID, IsAdmin: true},
		pathParameters: pathParams,
	}
	if body != "" {
		appCtx.body = []byte(body)
	}
	return appCtx
}

func TestHandler_ListMyOrgs(t *testing.T) {
	env := newHandlerTestEnv(t)
	require.NoError(t, env.orgRepo.UpsertMember(context.Background(), 1, env.userID, domain.OrgRoleOwner))

	appCtx := &fakeAppContext{claims: &domain.Claims{UserID: env.userID}}
	env.handler.ListMyOrgs(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	orgs, ok := appCtx.response.([]model.OrgResponse)
	require.True(t, ok)
	require.Len(t, orgs, 1)
	require.Equal(t, "default", orgs[0].Slug)
	require.Equal(t, domain.OrgRoleOwner, orgs[0].Role)
}

func TestHandler_ListMyOrgs_NoClaims(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := &fakeAppContext{}
	env.handler.ListMyOrgs(appCtx)

	require.Equal(t, http.StatusUnauthorized, appCtx.statusCode)
}

func TestHandler_ListOrgMembers(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"}, "")
	env.handler.ListOrgMembers(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	members, ok := appCtx.response.([]model.OrgMemberResponse)
	require.True(t, ok)
	require.NotEmpty(t, members, "seeded super admin is a member of the default org")
}

func TestHandler_ListOrgMembers_UnknownOrg(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "missing"}, "")
	env.handler.ListOrgMembers(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_AddOrgMember_ByUserIDAndEmail(t *testing.T) {
	env := newHandlerTestEnv(t)
	bob := env.createUser(t, "bob", "bob@example.com")
	carol := env.createUser(t, "carol", "carol@example.com")

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"},
		`{"user_id":"`+bob.ID.Base36()+`","role":"owner"}`)
	env.handler.AddOrgMember(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	member, ok := appCtx.response.(model.OrgMemberResponse)
	require.True(t, ok)
	require.Equal(t, domain.OrgRoleOwner, member.Role)
	require.Equal(t, bob.ID.Base36(), member.UserID)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"},
		`{"email":"carol@example.com"}`)
	env.handler.AddOrgMember(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	member, ok = appCtx.response.(model.OrgMemberResponse)
	require.True(t, ok)
	require.Equal(t, domain.OrgRoleMember, member.Role, "role defaults to member")
	require.Equal(t, carol.ID.Base36(), member.UserID)
}

func TestHandler_AddOrgMember_Validation(t *testing.T) {
	env := newHandlerTestEnv(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "both identifiers", body: `{"user_id":"1","email":"a@b.c"}`, want: http.StatusBadRequest},
		{name: "no identifier", body: `{}`, want: http.StatusBadRequest},
		{name: "invalid id", body: `{"user_id":"!!!"}`, want: http.StatusBadRequest},
		{name: "unknown user", body: `{"user_id":"` + snow.ID(999999).Base36() + `"}`, want: http.StatusNotFound},
		{name: "unknown email", body: `{"email":"ghost@example.com"}`, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"}, tt.body)
			env.handler.AddOrgMember(appCtx)
			require.Equal(t, tt.want, appCtx.statusCode)
		})
	}
}

func TestHandler_UpdateOrgMember(t *testing.T) {
	env := newHandlerTestEnv(t)
	bob := env.createUser(t, "bob", "bob@example.com")
	require.NoError(t, env.orgRepo.UpsertMember(context.Background(), 1, bob.ID, domain.OrgRoleMember))

	appCtx := adminAppCtx(env.userID,
		map[string]string{"org": "default", "user": bob.ID.Base36()}, `{"role":"owner"}`)
	env.handler.UpdateOrgMember(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	member, ok := appCtx.response.(model.OrgMemberResponse)
	require.True(t, ok)
	require.Equal(t, domain.OrgRoleOwner, member.Role)
}

func TestHandler_UpdateOrgMember_InvalidRole(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID,
		map[string]string{"org": "default", "user": snow.ID(1).Base36()}, `{"role":"viewer"}`)
	env.handler.UpdateOrgMember(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_RemoveOrgMember(t *testing.T) {
	env := newHandlerTestEnv(t)
	bob := env.createUser(t, "bob", "bob@example.com")
	require.NoError(t, env.orgRepo.UpsertMember(context.Background(), 1, bob.ID, domain.OrgRoleMember))

	appCtx := adminAppCtx(env.userID,
		map[string]string{"org": "default", "user": bob.ID.Base36()}, "")
	env.handler.RemoveOrgMember(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	_, err := env.orgRepo.MemberRole(context.Background(), 1, bob.ID)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestHandler_RemoveOrgMember_LastOwner(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID,
		map[string]string{"org": "default", "user": snow.ID(1).Base36()}, "")
	env.handler.RemoveOrgMember(appCtx)

	require.Equal(t, http.StatusConflict, appCtx.statusCode)
}

func TestHandler_GroupLifecycle(t *testing.T) {
	env := newHandlerTestEnv(t)
	bob := env.createUser(t, "bob", "bob@example.com")

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"},
		`{"name":"artists","description":"2d team"}`)
	env.handler.CreateGroup(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	group, ok := appCtx.response.(model.GroupResponse)
	require.True(t, ok)
	require.Equal(t, "artists", group.Name)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"}, "")
	env.handler.ListGroups(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	groups, ok := appCtx.response.([]model.GroupResponse)
	require.True(t, ok)
	require.Len(t, groups, 1)

	appCtx = adminAppCtx(env.userID,
		map[string]string{"org": "default", "group": group.ID}, `{"user_id":"`+bob.ID.Base36()+`"}`)
	env.handler.AddGroupMember(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "group": group.ID}, "")
	env.handler.GetGroup(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	detail, ok := appCtx.response.(model.GroupResponse)
	require.True(t, ok)
	require.Equal(t, []string{bob.ID.Base36()}, detail.MemberIDs)

	appCtx = adminAppCtx(env.userID,
		map[string]string{"org": "default", "group": group.ID, "user": bob.ID.Base36()}, "")
	env.handler.RemoveGroupMember(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
}

func TestHandler_GroupErrors(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default", "group": "!!!"}, "")
	env.handler.GetGroup(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "group": "999"}, "")
	env.handler.GetGroup(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID,
		map[string]string{"org": "default", "group": "999"}, `{"user_id":"1"}`)
	env.handler.AddGroupMember(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID,
		map[string]string{"org": "default", "group": "999"}, `{"user_id":"!!!"}`)
	env.handler.AddGroupMember(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"}, `{"name":"  "}`)
	env.handler.CreateGroup(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_UserAdminLifecycle(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	appCtx := adminAppCtx(env.userID, nil, `{"name":"carol","email":"carol@example.com","password":"password123"}`)
	env.handler.CreateUser(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	carol, ok := appCtx.response.(model.UserResponse)
	require.True(t, ok)
	require.Equal(t, "carol@example.com", carol.Email)
	require.False(t, carol.IsAdmin)

	appCtx = adminAppCtx(env.userID, nil, `{"name":"dave","email":"dave@example.com","password":"short"}`)
	env.handler.CreateUser(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, nil, "")
	env.handler.ListUsers(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	users, ok := appCtx.response.([]model.UserResponse)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(users), 2)

	appCtx = adminAppCtx(env.userID, map[string]string{"user": carol.ID}, `{"email":"carol2@example.com"}`)
	env.handler.UpdateUser(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	updated, ok := appCtx.response.(model.UserResponse)
	require.True(t, ok)
	require.Equal(t, "carol2@example.com", updated.Email)

	appCtx = adminAppCtx(env.userID, map[string]string{"user": carol.ID}, `{"password":"newpassword123"}`)
	env.handler.ResetUserPassword(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"user": carol.ID}, `{"is_admin":true}`)
	env.handler.UpdateUserFlags(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode, "only super admins change flags")

	appCtx = &fakeAppContext{
		claims:         &domain.Claims{UserID: env.userID, IsAdmin: true, IsSuperAdmin: true},
		pathParameters: map[string]string{"user": carol.ID},
		body:           []byte(`{"is_admin":true}`),
	}
	env.handler.UpdateUserFlags(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	flagged, ok := appCtx.response.(model.UserResponse)
	require.True(t, ok)
	require.True(t, flagged.IsAdmin)

	carolID, err := snow.ParseBase36(carol.ID)
	require.NoError(t, err)

	appCtx = adminAppCtx(env.userID, map[string]string{"user": carol.ID}, "")
	env.handler.DeleteUser(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	_, err = env.userRepo.GetByID(ctx, carolID)
	require.Error(t, err)

	appCtx = adminAppCtx(env.userID, map[string]string{"user": env.userID.Base36()}, "")
	env.handler.DeleteUser(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode, "cannot deactivate yourself")

	appCtx = adminAppCtx(env.userID, map[string]string{"user": "!!!"}, `{"email":"x@y.z"}`)
	env.handler.UpdateUser(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_SelfProfile(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := &fakeAppContext{
		claims: &domain.Claims{UserID: env.userID},
		body:   []byte(`{"name":"Alice Admin","photo_url":"https://example.com/a.png"}`),
	}
	env.handler.UpdateMyProfile(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	profile, ok := appCtx.response.(model.UserResponse)
	require.True(t, ok)
	require.Equal(t, "Alice Admin", profile.Name)
	require.Equal(t, "https://example.com/a.png", profile.PhotoUrl)

	appCtx = &fakeAppContext{
		claims: &domain.Claims{UserID: env.userID},
		body:   []byte(`{"name":"  "}`),
	}
	env.handler.UpdateMyProfile(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims: &domain.Claims{UserID: env.userID},
		body:   []byte(`{"old_password":"whatever","new_password":"longenough1"}`),
	}
	env.handler.ChangeMyPassword(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims: &domain.Claims{UserID: env.userID},
		body:   []byte(`{"old_password":"whatever","new_password":"short"}`),
	}
	env.handler.ChangeMyPassword(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{body: []byte(`{"name":"x"}`)}
	env.handler.UpdateMyProfile(appCtx)
	require.Equal(t, http.StatusUnauthorized, appCtx.statusCode)

	appCtx = &fakeAppContext{body: []byte(`{"old_password":"a","new_password":"longenough1"}`)}
	env.handler.ChangeMyPassword(appCtx)
	require.Equal(t, http.StatusUnauthorized, appCtx.statusCode)
}

func userAppCtx(userID snow.ID, pathParams map[string]string, body string) *fakeAppContext {
	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID},
		pathParameters: pathParams,
	}
	if body != "" {
		appCtx.body = []byte(body)
	}
	return appCtx
}

func canceledAppCtx(userID snow.ID, pathParams map[string]string, body string) *fakeAppContext {
	ctx, cancel := context.WithCancel(
		domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsAdmin: true}),
	)
	cancel()
	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID, IsAdmin: true},
		pathParameters: pathParams,
		ctx:            ctx,
	}
	if body != "" {
		appCtx.body = []byte(body)
	}
	return appCtx
}

func TestHandler_NonAdminHandlers_Denied(t *testing.T) {
	env := newHandlerTestEnv(t)

	tests := []struct {
		name   string
		params map[string]string
		body   string
		run    func(appCtx httpApp.AppContext)
	}{
		{name: "list org members", params: map[string]string{"org": "default"}, run: env.handler.ListOrgMembers},
		{name: "add org member", params: map[string]string{"org": "default"}, body: `{"user_id":"1"}`, run: env.handler.AddOrgMember},
		{name: "update org member", params: map[string]string{"org": "default", "user": "1"}, body: `{"role":"member"}`, run: env.handler.UpdateOrgMember},
		{name: "remove org member", params: map[string]string{"org": "default", "user": "1"}, run: env.handler.RemoveOrgMember},
		{name: "list groups", params: map[string]string{"org": "default"}, run: env.handler.ListGroups},
		{name: "create group", params: map[string]string{"org": "default"}, body: `{"name":"artists"}`, run: env.handler.CreateGroup},
		{name: "get group", params: map[string]string{"org": "default", "group": "999"}, run: env.handler.GetGroup},
		{name: "add group member", params: map[string]string{"org": "default", "group": "999"}, body: `{"user_id":"1"}`, run: env.handler.AddGroupMember},
		{name: "remove group member", params: map[string]string{"org": "default", "group": "999", "user": "1"}, run: env.handler.RemoveGroupMember},
		{name: "list users", run: env.handler.ListUsers},
		{name: "create user", body: `{"name":"x","email":"x@example.com","password":"password123"}`, run: env.handler.CreateUser},
		{name: "update user", params: map[string]string{"user": "1"}, body: `{"email":"x@example.com"}`, run: env.handler.UpdateUser},
		{name: "delete user", params: map[string]string{"user": "1"}, run: env.handler.DeleteUser},
		{name: "reset user password", params: map[string]string{"user": "1"}, body: `{"password":"password123"}`, run: env.handler.ResetUserPassword},
		{name: "update user flags", params: map[string]string{"user": "1"}, body: `{"is_admin":true}`, run: env.handler.UpdateUserFlags},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := userAppCtx(env.userID, tt.params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusForbidden, appCtx.statusCode)
		})
	}
}

func TestHandler_AdminHandlers_ContextError(t *testing.T) {
	env := newHandlerTestEnv(t)

	tests := []struct {
		name   string
		params map[string]string
		body   string
		run    func(appCtx httpApp.AppContext)
	}{
		{name: "list my orgs", run: env.handler.ListMyOrgs},
		{name: "list org members", params: map[string]string{"org": "default"}, run: env.handler.ListOrgMembers},
		{name: "add org member", params: map[string]string{"org": "default"}, body: `{"user_id":"1"}`, run: env.handler.AddOrgMember},
		{name: "update org member", params: map[string]string{"org": "default", "user": "1"}, body: `{"role":"member"}`, run: env.handler.UpdateOrgMember},
		{name: "remove org member", params: map[string]string{"org": "default", "user": "1"}, run: env.handler.RemoveOrgMember},
		{name: "list groups", params: map[string]string{"org": "default"}, run: env.handler.ListGroups},
		{name: "create group", params: map[string]string{"org": "default"}, body: `{"name":"artists"}`, run: env.handler.CreateGroup},
		{name: "get group", params: map[string]string{"org": "default", "group": "999"}, run: env.handler.GetGroup},
		{name: "add group member", params: map[string]string{"org": "default", "group": "999"}, body: `{"user_id":"1"}`, run: env.handler.AddGroupMember},
		{name: "remove group member", params: map[string]string{"org": "default", "group": "999", "user": "1"}, run: env.handler.RemoveGroupMember},
		{name: "me", run: env.handler.Me},
		{name: "update my profile", body: `{"name":"Alice"}`, run: env.handler.UpdateMyProfile},
		{name: "change my password", body: `{"old_password":"a","new_password":"longenough1"}`, run: env.handler.ChangeMyPassword},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, tt.params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}

func TestHandler_AdminHandlers_InvalidIDs(t *testing.T) {
	env := newHandlerTestEnv(t)

	tests := []struct {
		name string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "update org member", run: env.handler.UpdateOrgMember},
		{name: "remove org member", run: env.handler.RemoveOrgMember},
		{name: "add group member", run: env.handler.AddGroupMember},
		{name: "remove group member", run: env.handler.RemoveGroupMember},
		{name: "delete user", run: env.handler.DeleteUser},
		{name: "reset user password", run: env.handler.ResetUserPassword},
		{name: "update user flags", run: env.handler.UpdateUserFlags},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := adminAppCtx(env.userID, map[string]string{"org": "default", "user": "!!!", "group": "999"}, `{}`)
			tt.run(appCtx)
			require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
		})
	}
}
