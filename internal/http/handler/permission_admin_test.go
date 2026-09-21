package handler

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

func TestHandler_ProjectACLAdmin(t *testing.T) {
	env := newHandlerTestEnv(t)
	bob := env.createUser(t, "bob", "bob@example.com")
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"user_id":"` + bob.ID.Base36() + `","path_prefix":"assets/","permission":3}`),
	}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	rule, ok := appCtx.response.(model.PBACRuleResponse)
	require.True(t, ok)
	require.NotZero(t, rule.ID)
	require.Equal(t, bob.ID.Base36(), rule.UserID)
	require.Equal(t, "assets", rule.PathPrefix)
	require.Equal(t, uint64(3), rule.Permission)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListProjectRules(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	rules, ok := appCtx.response.([]model.PBACRuleResponse)
	require.True(t, ok)
	require.Len(t, rules, 1)

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: map[string]string{"org": "default", "project": "default", "id": strconv.FormatInt(rule.ID, 10)},
	}
	env.handler.DeleteProjectRule(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListProjectRules(appCtx)
	require.Empty(t, appCtx.response.([]model.PBACRuleResponse))

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"path_prefix":"docs","permission":1}`),
	}
	env.handler.SetProjectDefault(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "docs", appCtx.response.(model.PermissionEntry).PathPrefix)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListProjectDefaults(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Len(t, appCtx.response.([]model.PermissionEntry), 1)

	appCtx = &fakeAppContext{
		claims:          claims,
		pathParameters:  projectParams,
		queryParameters: map[string]string{"path": "docs"},
	}
	env.handler.DeleteProjectDefault(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListProjectDefaults(appCtx)
	require.Empty(t, appCtx.response.([]model.PermissionEntry))
}

func TestHandler_ProjectACLValidationAndAccess(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"path_prefix":"","permission":1}`)}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"user_id":"!!!","permission":1}`)}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"user_id":"` + snow.ID(999999).Base36() + `","permission":1}`)}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"user_id":"1","permission":0}`)}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: map[string]string{"org": "default", "project": "default", "id": "abc"},
	}
	env.handler.DeleteProjectRule(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	regular := &domain.Claims{UserID: env.userID}
	appCtx = &fakeAppContext{claims: regular, pathParameters: projectParams}
	env.handler.ListProjectRules(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: regular, pathParameters: projectParams, body: []byte(`{"user_id":"1","permission":1}`)}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: regular, pathParameters: projectParams, body: []byte(`{"path_prefix":"","permission":1}`)}
	env.handler.SetProjectDefault(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: regular, pathParameters: projectParams}
	env.handler.ListProjectDefaults(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

func TestHandler_ProjectACLContextError(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default", "id": "1"}

	tests := []struct {
		name string
		body string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "list rules", run: env.handler.ListProjectRules},
		{name: "create rule", body: `{"user_id":"1","permission":1}`, run: env.handler.CreateProjectRule},
		{name: "delete rule", run: env.handler.DeleteProjectRule},
		{name: "list defaults", run: env.handler.ListProjectDefaults},
		{name: "set default", body: `{"permission":1}`, run: env.handler.SetProjectDefault},
		{name: "delete default", run: env.handler.DeleteProjectDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}

func TestHandler_ProjectACLInvalidBodies(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	params := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: claims, pathParameters: params, body: []byte(`{"user_id":`)}
	env.handler.CreateProjectRule(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: params, body: []byte(`{"permission":`)}
	env.handler.SetProjectDefault(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: map[string]string{"org": "missing", "project": "default"}}
	env.handler.ListProjectRules(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:          claims,
		pathParameters:  params,
		queryParameters: map[string]string{"path": "docs"},
	}
	env.handler.DeleteProjectDefault(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
}
