package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/stretchr/testify/require"
)

type testRegistry struct {
	auth       *usecase.Auth
	user       *usecase.User
	common     *usecase.Common
	permission *usecase.Permission
	org        *usecase.Org
	group      *usecase.Group
}

func (r *testRegistry) Auth() *usecase.Auth             { return r.auth }
func (r *testRegistry) User() *usecase.User             { return r.user }
func (r *testRegistry) Common() *usecase.Common         { return r.common }
func (r *testRegistry) Permission() *usecase.Permission { return r.permission }
func (r *testRegistry) Org() *usecase.Org               { return r.org }
func (r *testRegistry) Group() *usecase.Group           { return r.group }

type stubPasswordHasher struct{}

func (stubPasswordHasher) Hash(_ string) (string, error) { return "hash", nil }
func (stubPasswordHasher) Compare(_, _ string) bool      { return true }

// TestAPIRoutes exercises the fully wired HTTP API. It must call SetupRoute
// exactly once per process: SetupSwagger registers on the default mux, which
// panics on duplicate registration.
func TestAPIRoutes(t *testing.T) {
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
		PhotoUrl: sql.NullString{},
		IsAdmin:  true,
	})
	require.NoError(t, err)

	node, _ := snow.NewNode(1)
	userRepo := sqlite.NewUserRepository(dbConn)
	groupRepo := sqlite.NewGroupRepository(dbConn)
	orgUc := usecase.NewOrg(sqlite.NewOrgRepository(dbConn))
	permissionUc := usecase.NewPermission(sqlite.NewPBACRepository(dbConn), userRepo, groupRepo, orgUc)
	reg := &testRegistry{
		auth:       usecase.NewAuth("test-secret", stubPasswordHasher{}, userRepo, sqlite.NewAuthRepository(dbConn)),
		user:       usecase.NewUser(node, userRepo, stubPasswordHasher{}),
		common:     usecase.NewCommon(sqlite.NewOrgRepository(dbConn), sqlite.NewProjectRepository(dbConn)),
		permission: permissionUc,
		org:        orgUc,
		group:      usecase.NewGroup(groupRepo, node, permissionUc, orgUc),
	}

	container := NewAPI(reg).SetupRoute()
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)

	loginAs := func(t *testing.T, email string) (model.LoginResponse, *http.Cookie) {
		t.Helper()

		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"`+email+`","password":"whatever"}`, nil, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var loginResponse model.LoginResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&loginResponse))

		var refresh *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				refresh = cookie
			}
		}
		require.NotNil(t, refresh)
		return loginResponse, refresh
	}
	login := func(t *testing.T) (model.LoginResponse, *http.Cookie) {
		t.Helper()
		return loginAs(t, "alice@example.com")
	}

	t.Run("login sets an httpOnly refresh cookie", func(t *testing.T) {
		loginResponse, refresh := login(t)

		require.Equal(t, "Bearer", loginResponse.TokenType)
		require.NotEmpty(t, loginResponse.AccessToken)
		require.Equal(t, 30*60, loginResponse.ExpiresIn)

		require.NotEmpty(t, refresh.Value)
		require.True(t, refresh.HttpOnly)
		require.True(t, refresh.Secure)
		require.Equal(t, http.SameSiteStrictMode, refresh.SameSite)
		require.Equal(t, "/api/v1/auth", refresh.Path)
		require.Greater(t, refresh.MaxAge, 0)

		parsed := parseLoginAccessToken(t, loginResponse.AccessToken, "test-secret")
		require.Equal(t, snow.ID(userID), parsed.UserID)
	})

	t.Run("refresh rotates the cookie", func(t *testing.T) {
		_, refresh := login(t)

		resp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{refresh}, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var refreshed model.LoginResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&refreshed))
		require.Equal(t, "Bearer", refreshed.TokenType)
		require.NotEmpty(t, refreshed.AccessToken)

		var rotated *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				rotated = cookie
			}
		}
		require.NotNil(t, rotated)
		require.NotEmpty(t, rotated.Value)
		require.NotEqual(t, refresh.Value, rotated.Value)

		parsed := parseLoginAccessToken(t, refreshed.AccessToken, "test-secret")
		require.Equal(t, snow.ID(userID), parsed.UserID)
	})

	t.Run("refresh without a cookie is unauthorized", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, nil, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "refresh token required", apiErr.Error)
	})

	t.Run("logout revokes the refresh token and clears the cookie", func(t *testing.T) {
		_, refresh := login(t)

		resp := doJSON(t, server.URL+"/api/v1/auth/logout", `{}`, []*http.Cookie{refresh}, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cleared *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				cleared = cookie
			}
		}
		require.NotNil(t, cleared)
		require.Equal(t, -1, cleared.MaxAge)

		replay := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{refresh}, "")
		defer replay.Body.Close()
		require.Equal(t, http.StatusNotFound, replay.StatusCode)
	})

	t.Run("stale refresh does not clobber a rotated cookie", func(t *testing.T) {
		_, first := login(t)

		rotatedResp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{first}, "")
		defer rotatedResp.Body.Close()
		require.Equal(t, http.StatusOK, rotatedResp.StatusCode)

		var second *http.Cookie
		for _, cookie := range rotatedResp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				second = cookie
			}
		}
		require.NotNil(t, second)

		staleResp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{first}, "")
		defer staleResp.Body.Close()
		require.Equal(t, http.StatusNotFound, staleResp.StatusCode)
		for _, cookie := range staleResp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				require.NotEqual(t, -1, cookie.MaxAge, "the losing tab must not clear the rotated cookie")
			}
		}

		okResp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{second}, "")
		defer okResp.Body.Close()
		require.Equal(t, http.StatusOK, okResp.StatusCode)
	})

	t.Run("login unknown user", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"ghost@example.com","password":"whatever"}`, nil, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "invalid email or password", apiErr.Error)
	})

	t.Run("login invalid json", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":`, nil, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "internal unknown error", apiErr.Error)
	})

	t.Run("cross-origin writes are rejected", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"alice@example.com","password":"whatever"}`, nil, "http://evil.example")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "cross-origin request rejected", apiErr.Error)
	})

	t.Run("same-origin writes are allowed", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"alice@example.com","password":"whatever"}`, nil, server.URL)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("me returns the authenticated profile", func(t *testing.T) {
		loginResponse, _ := login(t)

		resp := doGet(t, server.URL+"/api/v1/me", loginResponse.AccessToken)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var me model.UserResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
		require.Equal(t, snow.ID(userID).Base36(), me.ID)
		require.Equal(t, "alice", me.Name)
		require.Equal(t, "alice@example.com", me.Email)
		require.True(t, me.IsAdmin)
	})

	t.Run("me requires a token", func(t *testing.T) {
		resp := doGet(t, server.URL+"/api/v1/me", "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("me rejects an invalid token", func(t *testing.T) {
		resp := doGet(t, server.URL+"/api/v1/me", "not-a-token")
		defer resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("project permissions for an admin", func(t *testing.T) {
		loginResponse, _ := login(t)

		resp := doGet(t, server.URL+"/api/v1/orgs/default/projects/default/permissions/me", loginResponse.AccessToken)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var perms model.ProjectPermissionResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&perms))
		require.Equal(t, uint64(domain.PermissionAll), perms.ProjectPermission)
	})

	t.Run("project permissions reject unknown projects", func(t *testing.T) {
		loginResponse, _ := login(t)

		resp := doGet(t, server.URL+"/api/v1/orgs/default/projects/missing/permissions/me", loginResponse.AccessToken)
		defer resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("project permissions require a token", func(t *testing.T) {
		resp := doGet(t, server.URL+"/api/v1/orgs/default/projects/default/permissions/me", "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("organization membership and groups", func(t *testing.T) {
		aliceLogin, _ := login(t)
		superLogin, _ := loginAs(t, "supernipa")
		aliceID := snow.ID(userID).Base36()
		base := server.URL + "/api/v1/orgs/default"

		orgs := decodeBody[[]model.OrgResponse](t, doGet(t, server.URL+"/api/v1/orgs", aliceLogin.AccessToken))
		require.Empty(t, orgs, "alice starts outside the organization")

		addAlice := doMethod(t, http.MethodPost, base+"/members",
			`{"user_id":"`+aliceID+`","role":"owner"}`, superLogin.AccessToken)
		require.Equal(t, http.StatusOK, addAlice.StatusCode)
		aliceMembership := decodeBody[model.OrgMemberResponse](t, addAlice)
		require.Equal(t, "owner", aliceMembership.Role)
		require.Equal(t, "alice@example.com", aliceMembership.Email)
		require.NotNil(t, aliceMembership.JoinedAt)

		orgs = decodeBody[[]model.OrgResponse](t, doGet(t, server.URL+"/api/v1/orgs", aliceLogin.AccessToken))
		require.Len(t, orgs, 1)
		require.Equal(t, "default", orgs[0].Slug)
		require.Equal(t, "owner", orgs[0].Role)

		createBob := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"bob","email":"bob@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBob.StatusCode)
		bob := decodeBody[model.UserResponse](t, createBob)
		require.False(t, bob.IsAdmin)

		duplicate := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"bob2","email":"bob@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, duplicate.StatusCode)
		duplicate.Body.Close()

		addBob := doMethod(t, http.MethodPost, base+"/members",
			`{"email":"bob@example.com","role":"member"}`, superLogin.AccessToken)
		require.Equal(t, http.StatusOK, addBob.StatusCode)
		addBob.Body.Close()

		members := decodeBody[[]model.OrgMemberResponse](t, doGet(t, base+"/members", aliceLogin.AccessToken))
		require.Len(t, members, 3, "seeded super admin, alice and bob")

		bobLogin, _ := loginAs(t, "bob@example.com")
		denied := doGet(t, base+"/members", bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, denied.StatusCode)
		denied.Body.Close()
		deniedUsers := doGet(t, server.URL+"/api/v1/users", bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, deniedUsers.StatusCode)
		deniedUsers.Body.Close()
		bobOrgs := decodeBody[[]model.OrgResponse](t, doGet(t, server.URL+"/api/v1/orgs", bobLogin.AccessToken))
		require.Len(t, bobOrgs, 1)
		require.Equal(t, "member", bobOrgs[0].Role)

		promote := doMethod(t, http.MethodPatch, base+"/members/"+bob.ID, `{"role":"owner"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, promote.StatusCode)
		promote.Body.Close()
		ownerView := doGet(t, base+"/members", bobLogin.AccessToken)
		require.Equal(t, http.StatusOK, ownerView.StatusCode)
		ownerView.Body.Close()
		demote := doMethod(t, http.MethodPatch, base+"/members/"+bob.ID, `{"role":"member"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, demote.StatusCode)
		demote.Body.Close()

		createGroup := doMethod(t, http.MethodPost, base+"/groups",
			`{"name":"artists","description":"2d team"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createGroup.StatusCode)
		group := decodeBody[model.GroupResponse](t, createGroup)
		require.Equal(t, "artists", group.Name)

		groups := decodeBody[[]model.GroupResponse](t, doGet(t, base+"/groups", aliceLogin.AccessToken))
		require.Len(t, groups, 1)

		deniedGroups := doGet(t, base+"/groups", bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, deniedGroups.StatusCode)
		deniedGroups.Body.Close()

		addMember := doMethod(t, http.MethodPost, base+"/groups/"+group.ID+"/members",
			`{"user_id":"`+bob.ID+`"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, addMember.StatusCode)
		addMember.Body.Close()

		detail := decodeBody[model.GroupResponse](t, doGet(t, base+"/groups/"+group.ID, aliceLogin.AccessToken))
		require.Equal(t, []string{bob.ID}, detail.MemberIDs)

		removeMember := doMethod(t, http.MethodDelete, base+"/groups/"+group.ID+"/members/"+bob.ID, "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, removeMember.StatusCode)
		removeMember.Body.Close()

		removeBob := doMethod(t, http.MethodDelete, base+"/members/"+bob.ID, "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, removeBob.StatusCode)
		removeBob.Body.Close()
		bobOrgs = decodeBody[[]model.OrgResponse](t, doGet(t, server.URL+"/api/v1/orgs", bobLogin.AccessToken))
		require.Empty(t, bobOrgs)
	})

	t.Run("user administration", func(t *testing.T) {
		superLogin, _ := loginAs(t, "supernipa")

		createCarol := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"carol","email":"carol@example.com","password":"password123"}`, superLogin.AccessToken)
		require.Equal(t, http.StatusOK, createCarol.StatusCode)
		carol := decodeBody[model.UserResponse](t, createCarol)

		updateEmail := doMethod(t, http.MethodPatch, server.URL+"/api/v1/users/"+carol.ID,
			`{"email":"carol2@example.com"}`, superLogin.AccessToken)
		require.Equal(t, http.StatusOK, updateEmail.StatusCode)
		updated := decodeBody[model.UserResponse](t, updateEmail)
		require.Equal(t, "carol2@example.com", updated.Email)

		flags := doMethod(t, http.MethodPatch, server.URL+"/api/v1/users/"+carol.ID+"/admin",
			`{"is_admin":true,"is_super_admin":false}`, superLogin.AccessToken)
		require.Equal(t, http.StatusOK, flags.StatusCode)
		flagged := decodeBody[model.UserResponse](t, flags)
		require.True(t, flagged.IsAdmin)

		reset := doMethod(t, http.MethodPost, server.URL+"/api/v1/users/"+carol.ID+"/password",
			`{"password":"newpassword123"}`, superLogin.AccessToken)
		require.Equal(t, http.StatusOK, reset.StatusCode)
		reset.Body.Close()

		selfDemote := doMethod(t, http.MethodPatch, server.URL+"/api/v1/users/1/admin",
			`{"is_admin":true,"is_super_admin":false}`, superLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, selfDemote.StatusCode)
		selfDemote.Body.Close()

		selfDelete := doMethod(t, http.MethodDelete, server.URL+"/api/v1/users/1", "", superLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, selfDelete.StatusCode)
		selfDelete.Body.Close()

		deactivate := doMethod(t, http.MethodDelete, server.URL+"/api/v1/users/"+carol.ID, "", superLogin.AccessToken)
		require.Equal(t, http.StatusOK, deactivate.StatusCode)
		deactivate.Body.Close()

		users := decodeBody[[]model.UserResponse](t, doGet(t, server.URL+"/api/v1/users", superLogin.AccessToken))
		for _, user := range users {
			require.NotEqual(t, carol.ID, user.ID)
		}

		reactivate := doPostJSON(t, server.URL+"/api/v1/auth/login", `{"email":"alice@example.com","password":"whatever"}`)
		defer reactivate.Body.Close()
		require.Equal(t, http.StatusOK, reactivate.StatusCode)
	})

	t.Run("self profile and password", func(t *testing.T) {
		aliceLogin, _ := login(t)

		update := doMethod(t, http.MethodPatch, server.URL+"/api/v1/me",
			`{"name":"Alice Admin","photo_url":"https://example.com/a.png"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, update.StatusCode)
		profile := decodeBody[model.UserResponse](t, update)
		require.Equal(t, "Alice Admin", profile.Name)
		require.Equal(t, "https://example.com/a.png", profile.PhotoUrl)

		short := doMethod(t, http.MethodPost, server.URL+"/api/v1/me/password",
			`{"old_password":"whatever","new_password":"short"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, short.StatusCode)
		short.Body.Close()

		change := doMethod(t, http.MethodPost, server.URL+"/api/v1/me/password",
			`{"old_password":"whatever","new_password":"longenough1"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, change.StatusCode)
		change.Body.Close()
	})

	t.Run("openapi doc is served", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/docs/api.json")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func doJSON(t *testing.T, url, body string, cookies []*http.Cookie, origin string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func doGet(t *testing.T, url, accessToken string) *http.Response {
	t.Helper()
	return doMethod(t, http.MethodGet, url, "", accessToken)
}

func doMethod(t *testing.T, method, url, body, accessToken string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func doPostJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()

	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	return resp
}

func parseLoginAccessToken(t *testing.T, token string, secret string) *domain.Claims {
	t.Helper()

	claims := domain.Claims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	return &claims
}
