package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/stretchr/testify/require"
)

type fakeAppContext struct {
	body            []byte
	statusCode      int
	response        any
	cookies         map[string]*http.Cookie
	claims          *domain.Claims
	pathParameters  map[string]string
	ctx             context.Context
	contentType     string
	raw             []byte
	queryParameters map[string]string
}

func (f *fakeAppContext) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	if f.claims != nil {
		return domain.ContextWithClaim(context.Background(), *f.claims)
	}
	return context.Background()
}

func (f *fakeAppContext) Claims() *domain.Claims { return f.claims }

func (f *fakeAppContext) PathParameter(name string) string {
	return f.pathParameters[name]
}

func (f *fakeAppContext) QueryParameter(name string) string {
	return f.queryParameters[name]
}

func (f *fakeAppContext) ReadJson(v any) error {
	return json.Unmarshal(f.body, v)
}

func (f *fakeAppContext) WriteJson(statusCode int, v any) error {
	f.statusCode = statusCode
	f.response = v
	return nil
}

func (f *fakeAppContext) WriteBytes(statusCode int, contentType string, data []byte) {
	f.statusCode = statusCode
	f.contentType = contentType
	f.raw = data
}

func (f *fakeAppContext) SetCookie(cookie *http.Cookie) {
	if f.cookies == nil {
		f.cookies = map[string]*http.Cookie{}
	}
	f.cookies[cookie.Name] = cookie
}

func (f *fakeAppContext) Cookie(name string) (*http.Cookie, error) {
	if cookie, ok := f.cookies[name]; ok {
		return cookie, nil
	}
	return nil, http.ErrNoCookie
}

func (f *fakeAppContext) HandleError(err error) {
	apiErr, ok := err.(*domain.Error)
	if ok {
		f.statusCode = apiErr.Code
		f.response = model.NewAPIError(apiErr.Message)
		return
	}
	f.statusCode = http.StatusInternalServerError
	f.response = model.NewAPIError("internal unknown error")
}

type handlerRegistry struct {
	auth         *usecase.Auth
	user         *usecase.User
	common       *usecase.Common
	permission   *usecase.Permission
	org          *usecase.Org
	group        *usecase.Group
	project      *usecase.Project
	branch       *usecase.Branch
	mergeRequest *usecase.MergeRequest
}

func (r *handlerRegistry) Auth() *usecase.Auth             { return r.auth }
func (r *handlerRegistry) User() *usecase.User             { return r.user }
func (r *handlerRegistry) Common() *usecase.Common         { return r.common }
func (r *handlerRegistry) Permission() *usecase.Permission { return r.permission }
func (r *handlerRegistry) Org() *usecase.Org               { return r.org }
func (r *handlerRegistry) Group() *usecase.Group           { return r.group }
func (r *handlerRegistry) Project() *usecase.Project       { return r.project }
func (r *handlerRegistry) Branch() *usecase.Branch         { return r.branch }
func (r *handlerRegistry) MergeRequest() *usecase.MergeRequest {
	return r.mergeRequest
}

type stubPasswordHasher struct{}

func (stubPasswordHasher) Hash(_ string) (string, error) { return "hash", nil }
func (stubPasswordHasher) Compare(_, _ string) bool      { return true }

type handlerTestEnv struct {
	handler    *Handler
	authRepo   *sqlite.Auth
	pbacRepo   *sqlite.PBAC
	userRepo   *sqlite.User
	orgRepo    *sqlite.OrgRepository
	groupRepo  *sqlite.Group
	node       snow.Node
	userID     snow.ID
	branchRepo *sqlite.BranchRepository
	chunkStore *storage.LocalStore
	pusher     *usecase.Push
	chunkUc    *usecase.Chunk
}

func newHandlerTestEnv(t *testing.T) *handlerTestEnv {
	t.Helper()

	dbConn, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dbConn.Close()) })
	dbConn.SetMaxOpenConns(1)
	require.NoError(t, database.MigrateUp(dbConn, "sqlite3"))

	q := sqlcSqlite.New(dbConn)
	userID, err := q.UserCreate(context.Background(), sqlcSqlite.UserCreateParams{
		Name:     "alice",
		Email:    "alice@example.com",
		Password: "hashed-password",
		IsAdmin:  true,
	})
	require.NoError(t, err)

	authRepo := sqlite.NewAuthRepository(dbConn)
	userRepo := sqlite.NewUserRepository(dbConn)
	pbacRepo := sqlite.NewPBACRepository(dbConn)
	groupRepo := sqlite.NewGroupRepository(dbConn)
	node, _ := snow.NewNode(1)
	orgRepo := sqlite.NewOrgRepository(dbConn)
	orgUc := usecase.NewOrg(orgRepo)
	permissionUc := usecase.NewPermission(pbacRepo, userRepo, groupRepo, orgUc)
	projectUc := usecase.NewProject(sqlite.NewProjectRepository(dbConn), node, permissionUc, orgUc)
	branchRepo := sqlite.NewBranchRepository(dbConn)
	chunkStore, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = chunkStore.Close() })
	pushRepo := sqlite.NewPushRepository(dbConn)
	branchUc := usecase.NewBranchWithChunks(permissionUc, branchRepo, node, chunkStore)
	pusher := usecase.NewPush(permissionUc, branchRepo, pushRepo, node)
	chunkUc := usecase.NewChunk(pushRepo, chunkStore)
	mergeRequestUc := usecase.NewMergeRequest(
		sqlite.NewMergeRequestRepository(dbConn), branchRepo, permissionUc, branchUc, node,
	)
	reg := &handlerRegistry{
		auth:         usecase.NewAuth("test-secret", stubPasswordHasher{}, userRepo, authRepo),
		user:         usecase.NewUser(node, userRepo, stubPasswordHasher{}),
		common:       usecase.NewCommon(orgRepo, sqlite.NewProjectRepository(dbConn)),
		permission:   permissionUc,
		org:          orgUc,
		group:        usecase.NewGroup(groupRepo, node, permissionUc, orgUc),
		project:      projectUc,
		branch:       branchUc,
		mergeRequest: mergeRequestUc,
	}
	return &handlerTestEnv{
		handler:    NewHandler(reg),
		authRepo:   authRepo,
		pbacRepo:   pbacRepo,
		userRepo:   userRepo,
		orgRepo:    orgRepo,
		groupRepo:  groupRepo,
		node:       node,
		userID:     snow.ID(userID),
		branchRepo: branchRepo,
		chunkStore: chunkStore,
		pusher:     pusher,
		chunkUc:    chunkUc,
	}
}

func newHandlerTestSetup(t *testing.T) (*Handler, *sqlite.Auth, snow.ID) {
	t.Helper()

	env := newHandlerTestEnv(t)
	return env.handler, env.authRepo, env.userID
}

func TestNewHandler(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)
	require.NotNil(t, h)
}

func TestHandler_AuthLogin(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{"email":"alice@example.com","password":"whatever"}`)}
	h.AuthLogin(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	loginResponse, ok := appCtx.response.(model.LoginResponse)
	require.True(t, ok)
	require.Equal(t, "Bearer", loginResponse.TokenType)
	require.NotEmpty(t, loginResponse.AccessToken)
	require.Equal(t, 30*60, loginResponse.ExpiresIn)

	cookie, ok := appCtx.cookies[refreshCookieName]
	require.True(t, ok)
	require.NotEmpty(t, cookie.Value)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, refreshCookiePath, cookie.Path)
	require.Equal(t, int((60 * 24 * time.Hour).Seconds()), cookie.MaxAge)

	claims := parseHandlerAccessToken(t, loginResponse.AccessToken, "test-secret")
	require.Equal(t, userID, claims.UserID)
}

func TestHandler_AuthLogin_UnknownUser(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{"email":"ghost@example.com","password":"whatever"}`)}
	h.AuthLogin(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "invalid email or password", apiErr.Error)
	require.Empty(t, appCtx.cookies)
}

func TestHandler_AuthLogin_InvalidJson(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{"email":`)}
	h.AuthLogin(appCtx)

	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "internal unknown error", apiErr.Error)
}

func TestHandler_AuthRefreshToken(t *testing.T) {
	h, authRepo, userID := newHandlerTestSetup(t)

	require.NoError(t, authRepo.SaveRefreshToken(
		context.Background(), userID, "valid-refresh-token", time.Now().Add(time.Hour)))

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "valid-refresh-token"}},
	}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	refreshed, ok := appCtx.response.(model.LoginResponse)
	require.True(t, ok)
	require.Equal(t, "Bearer", refreshed.TokenType)
	require.NotEmpty(t, refreshed.AccessToken)

	cookie, ok := appCtx.cookies[refreshCookieName]
	require.True(t, ok)
	require.NotEmpty(t, cookie.Value)
	require.NotEqual(t, "valid-refresh-token", cookie.Value, "refresh tokens rotate")
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
}

func TestHandler_AuthRefreshToken_MissingCookie(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{}`)}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusUnauthorized, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "refresh token required", apiErr.Error)
}

func TestHandler_AuthRefreshToken_Expired(t *testing.T) {
	h, authRepo, userID := newHandlerTestSetup(t)

	require.NoError(t, authRepo.SaveRefreshToken(
		context.Background(), userID, "expired-refresh-token", time.Now().Add(-time.Hour)))

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "expired-refresh-token"}},
	}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "refresh token expired", apiErr.Error)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge, "invalid refresh cookies are cleared")
}

func TestHandler_AuthRefreshToken_UnknownToken(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "unknown"}},
	}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "record not found", apiErr.Error)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, "unknown", cookie.Value, "another tab may hold the rotated cookie")
	require.NotEqual(t, -1, cookie.MaxAge)
}

func TestHandler_AuthLogout(t *testing.T) {
	h, authRepo, userID := newHandlerTestSetup(t)
	ctx := context.Background()

	require.NoError(t, authRepo.SaveRefreshToken(ctx, userID, "logout-token", time.Now().Add(time.Hour)))

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "logout-token"}},
	}
	h.AuthLogout(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	message, ok := appCtx.response.(model.MessageResponse)
	require.True(t, ok)
	require.Equal(t, "logged out", message.Message)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge)

	_, err := authRepo.GetAndDeleteRefreshToken(ctx, "logout-token")
	require.True(t, domain.IsErrorNotFound(err), "logout revokes the refresh token")
}

func TestHandler_AuthLogout_NoCookie(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{}`)}
	h.AuthLogout(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge)
}

func TestHandler_Me(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{claims: &domain.Claims{UserID: userID}}
	h.Me(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	me, ok := appCtx.response.(model.UserResponse)
	require.True(t, ok)
	require.Equal(t, userID.Base36(), me.ID)
	require.Equal(t, "alice", me.Name)
	require.Equal(t, "alice@example.com", me.Email)
	require.True(t, me.IsAdmin)
	require.False(t, me.IsSuperAdmin)
}

func TestHandler_Me_NoClaims(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{}
	h.Me(appCtx)

	require.Equal(t, http.StatusUnauthorized, appCtx.statusCode)
}

func TestHandler_Me_UnknownUser(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{claims: &domain.Claims{UserID: snow.ID(999)}}
	h.Me(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_MyProjectPermissions_Admin(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID, IsAdmin: true},
		pathParameters: map[string]string{"org": "default", "project": "default"},
	}
	h.MyProjectPermissions(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	resp, ok := appCtx.response.(model.ProjectPermissionResponse)
	require.True(t, ok)
	require.Equal(t, uint64(domain.PermissionAll), resp.ProjectPermission)
	require.Empty(t, resp.Rules)
	require.Empty(t, resp.Defaults)
}

func TestHandler_MyProjectPermissions_UnknownProject(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID},
		pathParameters: map[string]string{"org": "default", "project": "missing"},
	}
	h.MyProjectPermissions(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_MyProjectPermissions_WithRulesAndDefaults(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()
	projectID := snow.ID(1)
	userID := env.userID

	_, err := env.pbacRepo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)
	_, err = env.pbacRepo.CreateRule(ctx, domain.PBACRule{
		UserID: &userID, OrgID: 1, ProjectID: &projectID,
		PathPrefix: "assets", Permission: domain.PermissionRead | domain.PermissionWrite,
	})
	require.NoError(t, err)

	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID},
		pathParameters: map[string]string{"org": "default", "project": "default"},
	}
	env.handler.MyProjectPermissions(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	resp, ok := appCtx.response.(model.ProjectPermissionResponse)
	require.True(t, ok)
	require.Equal(t, uint64(domain.PermissionRead|domain.PermissionWrite), resp.ProjectPermission)
	require.Len(t, resp.Rules, 1)
	require.Equal(t, "assets", resp.Rules[0].PathPrefix)
	require.Equal(t, uint64(domain.PermissionRead|domain.PermissionWrite), resp.Rules[0].Permission)
	require.Len(t, resp.Defaults, 1)
	require.Equal(t, uint64(domain.PermissionRead), resp.Defaults[0].Permission)
}

func TestHandler_MyProjectPermissions_NoClaims(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		pathParameters: map[string]string{"org": "default", "project": "default"},
	}
	h.MyProjectPermissions(appCtx)

	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

var _ httpApp.AppContext = (*fakeAppContext)(nil)

func parseHandlerAccessToken(t *testing.T, token string, secret string) *domain.Claims {
	t.Helper()

	claims := domain.Claims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	return &claims
}
