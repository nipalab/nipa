package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/webhook"
	"github.com/stretchr/testify/require"
)

type testRegistry struct {
	auth          *usecase.Auth
	user          *usecase.User
	common        *usecase.Common
	permission    *usecase.Permission
	org           *usecase.Org
	group         *usecase.Group
	project       *usecase.Project
	branch        *usecase.Branch
	tag           *usecase.Tag
	chunk         *usecase.Chunk
	mergeRequest  *usecase.MergeRequest
	review        *usecase.MergeRequestReview
	check         *usecase.MergeRequestCheck
	fileLock      *usecase.FileLock
	webhook       *usecase.Webhook
	emailDelivery *usecase.EmailDelivery
}

func (r *testRegistry) Auth() *usecase.Auth             { return r.auth }
func (r *testRegistry) User() *usecase.User             { return r.user }
func (r *testRegistry) Common() *usecase.Common         { return r.common }
func (r *testRegistry) Permission() *usecase.Permission { return r.permission }
func (r *testRegistry) Org() *usecase.Org               { return r.org }
func (r *testRegistry) Group() *usecase.Group           { return r.group }
func (r *testRegistry) Project() *usecase.Project       { return r.project }
func (r *testRegistry) Branch() *usecase.Branch         { return r.branch }
func (r *testRegistry) Tag() *usecase.Tag               { return r.tag }
func (r *testRegistry) Chunk() *usecase.Chunk           { return r.chunk }
func (r *testRegistry) MergeRequest() *usecase.MergeRequest {
	return r.mergeRequest
}
func (r *testRegistry) MergeRequestReview() *usecase.MergeRequestReview {
	return r.review
}

func (r *testRegistry) MergeRequestCheck() *usecase.MergeRequestCheck {
	return r.check
}

func (r *testRegistry) FileLock() *usecase.FileLock { return r.fileLock }

func (r *testRegistry) Webhook() *usecase.Webhook { return r.webhook }

func (r *testRegistry) EmailDelivery() *usecase.EmailDelivery { return r.emailDelivery }

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
	pbacRepo := sqlite.NewPBACRepository(dbConn)
	orgUc := usecase.NewOrg(sqlite.NewOrgRepository(dbConn), node)
	permissionUc := usecase.NewPermission(pbacRepo, userRepo, groupRepo, orgUc)
	projectUc := usecase.NewProject(sqlite.NewProjectRepository(dbConn), node, permissionUc, orgUc)
	branchRepo := sqlite.NewBranchRepository(dbConn)
	pushRepo := sqlite.NewPushRepository(dbConn)
	chunkStore, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = chunkStore.Close() })
	branchUc := usecase.NewBranchWithChunks(permissionUc, branchRepo, node, chunkStore)
	reviewUc := usecase.NewMergeRequestReview(
		sqlite.NewMergeRequestReviewRepository(dbConn),
		sqlite.NewMergeRequestRepository(dbConn),
		branchRepo, branchUc, permissionUc, userRepo, node,
	)
	pusher := usecase.NewPush(permissionUc, branchRepo, pushRepo, node).WithReviews(reviewUc)
	chunkUc := usecase.NewChunk(pushRepo, chunkStore, usecase.ChunkTransferConfig{
		SigningKey:  "test-chunk-signing-key",
		PresignTTL:  time.Hour,
		MaxPageSize: 1000,
	})
	mergeRequestUc := usecase.NewMergeRequest(
		sqlite.NewMergeRequestRepository(dbConn), branchRepo, permissionUc, branchUc, node, dbtx.NewTransactor(dbConn),
	).WithReview(reviewUc)
	fileLockUc := usecase.NewFileLock(sqlite.NewFileLockRepository(dbConn), branchRepo, permissionUc, node)
	webhookRepo := sqlite.NewWebhookRepository(dbConn)
	webhookDispatcher := webhook.NewDispatcher(webhookRepo, webhook.NewClient(webhook.ClientConfig{
		EgressAllowlist: []string{"127.0.0.1"},
	}), node, webhook.Config{
		MaxAttempts:    3,
		InitialBackoff: 5 * time.Millisecond,
		BackoffFactor:  2,
		SweepInterval:  10 * time.Millisecond,
		RetryBatch:     100,
		Workers:        2,
	})
	webhookDispatcher.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = webhookDispatcher.Stop(ctx)
	})
	hookEmitter := usecase.NewHookEmitter(webhookRepo, sqlite.NewProjectRepository(dbConn), sqlite.NewOrgRepository(dbConn), branchRepo, userRepo, webhookDispatcher)
	branchUc = branchUc.WithHooks(hookEmitter)
	mergeRequestUc = mergeRequestUc.WithHooks(hookEmitter)
	reviewUc = reviewUc.WithHooks(hookEmitter)
	checkUc := usecase.NewMergeRequestCheck(
		sqlite.NewMergeRequestCheckRepository(dbConn),
		sqlite.NewMergeRequestRepository(dbConn),
		branchRepo,
		permissionUc,
		node,
	).WithHooks(hookEmitter)
	mergeRequestUc = mergeRequestUc.WithChecks(checkUc)
	pusher = pusher.WithHooks(hookEmitter)
	tagUc := usecase.NewTag(permissionUc, sqlite.NewTagRepository(dbConn), branchRepo, node).WithHooks(hookEmitter)
	reg := &testRegistry{
		auth:          usecase.NewAuth("test-secret", stubPasswordHasher{}, userRepo, sqlite.NewAuthRepository(dbConn)),
		user:          usecase.NewUser(node, userRepo, stubPasswordHasher{}),
		common:        usecase.NewCommon(sqlite.NewOrgRepository(dbConn), sqlite.NewProjectRepository(dbConn)),
		permission:    permissionUc,
		org:           orgUc,
		group:         usecase.NewGroup(groupRepo, node, permissionUc, orgUc),
		project:       projectUc,
		branch:        branchUc,
		tag:           tagUc,
		chunk:         chunkUc,
		mergeRequest:  mergeRequestUc,
		review:        reviewUc,
		check:         checkUc,
		fileLock:      fileLockUc,
		webhook:       usecase.NewWebhook(webhookRepo, permissionUc, userRepo, webhookDispatcher, node),
		emailDelivery: usecase.NewEmailDelivery(sqlite.NewEmailRepository(dbConn), permissionUc),
	}

	seedPushTo := func(t *testing.T, branch, baseCommitID string, files map[string]string) *domain.PushResult {
		t.Helper()

		pushCtx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(userID), IsAdmin: true})
		pushFiles := make([]*domain.PushFile, 0, len(files))
		for path, content := range files {
			chunks, err := chunker.ChunkAll([]byte(content))
			require.NoError(t, err)
			hashes := make([]domain.Hash, 0, len(chunks))
			for _, c := range chunks {
				_, err := chunkUc.Upload(pushCtx, c.Hash, c.Data)
				require.NoError(t, err)
				hashes = append(hashes, c.Hash)
			}
			pushFiles = append(pushFiles, &domain.PushFile{
				Path:        path,
				Mode:        0o644,
				SizeBytes:   int64(len(content)),
				FileHash:    treehash.FileHash(hashes),
				ChunkHashes: hashes,
			})
		}
		result, err := pusher.Push(pushCtx, snow.ID(1), branch, "", "seed", pushFiles, nil, "", baseCommitID)
		require.NoError(t, err)
		return result
	}
	seedBrowserFiles := func(t *testing.T, files map[string]string) *domain.PushResult {
		t.Helper()
		return seedPushTo(t, "main", "", files)
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

	t.Run("refresh and logout accept a request without content type", func(t *testing.T) {
		_, refresh := login(t)

		refreshed := doMethodCookies(t, http.MethodPost, server.URL+"/api/v1/auth/refresh", "", []*http.Cookie{refresh})
		require.Equal(t, http.StatusOK, refreshed.StatusCode)
		require.NoError(t, refreshed.Body.Close())

		var rotated *http.Cookie
		for _, cookie := range refreshed.Cookies() {
			if cookie.Name == "nipa_refresh" {
				rotated = cookie
			}
		}
		require.NotNil(t, rotated)

		loggedOut := doMethodCookies(t, http.MethodPost, server.URL+"/api/v1/auth/logout", "", []*http.Cookie{rotated})
		require.Equal(t, http.StatusOK, loggedOut.StatusCode)
		require.NoError(t, loggedOut.Body.Close())
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
		superLogin, _ := loginAs(t, "nipa")
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

		createOrg := doMethod(t, http.MethodPost, server.URL+"/api/v1/orgs",
			`{"name":"Acme Corp"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createOrg.StatusCode)
		acme := decodeBody[model.OrgResponse](t, createOrg)
		require.NotEmpty(t, acme.ID)
		require.Equal(t, "Acme Corp", acme.Name)
		require.Equal(t, "acme-corp", acme.Slug)
		require.Equal(t, "owner", acme.Role)

		duplicateOrg := doMethod(t, http.MethodPost, server.URL+"/api/v1/orgs",
			`{"name":"Acme","slug":"acme-corp"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, duplicateOrg.StatusCode)
		duplicateOrg.Body.Close()

		unauthenticatedOrg := doMethod(t, http.MethodPost, server.URL+"/api/v1/orgs",
			`{"name":"Anon"}`, "")
		require.Equal(t, http.StatusUnauthorized, unauthenticatedOrg.StatusCode)
		unauthenticatedOrg.Body.Close()

		orgs = decodeBody[[]model.OrgResponse](t, doGet(t, server.URL+"/api/v1/orgs", aliceLogin.AccessToken))
		require.Len(t, orgs, 2)

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
		require.Equal(t, int64(0), groups[0].MemberCount, "new group has no members yet")

		deniedGroups := doGet(t, base+"/groups", bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, deniedGroups.StatusCode)
		deniedGroups.Body.Close()

		addMember := doMethod(t, http.MethodPost, base+"/groups/"+group.ID+"/members",
			`{"user_id":"`+bob.ID+`"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, addMember.StatusCode)
		addMember.Body.Close()

		groups = decodeBody[[]model.GroupResponse](t, doGet(t, base+"/groups", aliceLogin.AccessToken))
		require.Len(t, groups, 1)
		require.Equal(t, int64(1), groups[0].MemberCount, "list reports the resolved member count")
		require.Empty(t, groups[0].Members, "the list does not expand member details")

		detail := decodeBody[model.GroupResponse](t, doGet(t, base+"/groups/"+group.ID, aliceLogin.AccessToken))
		require.Equal(t, []string{bob.ID}, detail.MemberIDs)
		require.Equal(t, []model.GroupMemberResponse{
			{UserID: bob.ID, Name: "bob", Email: "bob@example.com"},
		}, detail.Members)
		require.Equal(t, int64(1), detail.MemberCount)

		removeMember := doMethod(t, http.MethodDelete, base+"/groups/"+group.ID+"/members/"+bob.ID, "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, removeMember.StatusCode)
		removeMember.Body.Close()

		groups = decodeBody[[]model.GroupResponse](t, doGet(t, base+"/groups", aliceLogin.AccessToken))
		require.Len(t, groups, 1)
		require.Equal(t, int64(0), groups[0].MemberCount, "count drops back after removal")

		removeBob := doMethod(t, http.MethodDelete, base+"/members/"+bob.ID, "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, removeBob.StatusCode)
		removeBob.Body.Close()
		bobOrgs = decodeBody[[]model.OrgResponse](t, doGet(t, server.URL+"/api/v1/orgs", bobLogin.AccessToken))
		require.Empty(t, bobOrgs)
	})

	t.Run("user administration", func(t *testing.T) {
		superLogin, _ := loginAs(t, "nipa")

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
			`{"name":"Alice Admin","photo_url":"https://example.com/a.png","notify_email":false}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, update.StatusCode)
		profile := decodeBody[model.UserResponse](t, update)
		require.Equal(t, "Alice Admin", profile.Name)
		require.Equal(t, "https://example.com/a.png", profile.PhotoUrl)
		require.False(t, profile.NotifyEmail)

		me := decodeBody[model.UserResponse](t, doGet(t, server.URL+"/api/v1/me", aliceLogin.AccessToken))
		require.False(t, me.NotifyEmail)

		reenable := doMethod(t, http.MethodPatch, server.URL+"/api/v1/me",
			`{"name":"Alice Admin","notify_email":true}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, reenable.StatusCode)
		require.True(t, decodeBody[model.UserResponse](t, reenable).NotifyEmail)

		short := doMethod(t, http.MethodPost, server.URL+"/api/v1/me/password",
			`{"old_password":"whatever","new_password":"short"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, short.StatusCode)
		short.Body.Close()

		change := doMethod(t, http.MethodPost, server.URL+"/api/v1/me/password",
			`{"old_password":"whatever","new_password":"longenough1"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, change.StatusCode)
		change.Body.Close()
	})

	t.Run("project lifecycle", func(t *testing.T) {
		aliceLogin, _ := login(t)
		bobLogin, _ := loginAs(t, "bob@example.com")
		base := server.URL + "/api/v1/orgs/default/projects"

		create := doMethod(t, http.MethodPost, base,
			`{"name":"My Game","description":"fun"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, create.StatusCode)
		project := decodeBody[model.ProjectResponse](t, create)
		require.Equal(t, "my-game", project.Slug)
		require.Equal(t, "My Game", project.Name)

		projects := decodeBody[[]model.ProjectResponse](t, doGet(t, base, aliceLogin.AccessToken))
		require.Len(t, projects, 2, "seeded default project plus my-game")

		hidden := doGet(t, base+"/my-game", bobLogin.AccessToken)
		require.Equal(t, http.StatusNotFound, hidden.StatusCode, "projects without read access look missing")
		hidden.Body.Close()

		bobProjects := decodeBody[[]model.ProjectResponse](t, doGet(t, base, bobLogin.AccessToken))
		require.Empty(t, bobProjects)

		update := doMethod(t, http.MethodPatch, base+"/my-game",
			`{"name":"My Game 2","description":"v2"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, update.StatusCode)
		updated := decodeBody[model.ProjectResponse](t, update)
		require.Equal(t, "My Game 2", updated.Name)
		require.Equal(t, "my-game", updated.Slug)

		denied := doMethod(t, http.MethodPost, base, `{"name":"Nope"}`, bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, denied.StatusCode)
		denied.Body.Close()

		remove := doMethod(t, http.MethodDelete, base+"/my-game", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, remove.StatusCode)
		remove.Body.Close()

		gone := doGet(t, base+"/my-game", aliceLogin.AccessToken)
		require.Equal(t, http.StatusNotFound, gone.StatusCode)
		gone.Body.Close()
	})

	t.Run("repository browser", func(t *testing.T) {
		seed := seedBrowserFiles(t, map[string]string{
			"public/a.txt":   "hello",
			"secret/key.bin": "top secret",
		})
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default"

		tree := decodeBody[model.TreeResponse](t, doGet(t, base+"/tree?rev=main", aliceLogin.AccessToken))
		require.Len(t, tree.Entries, 2)

		subtree := decodeBody[model.TreeResponse](t, doGet(t, base+"/tree?rev=main&path=public", aliceLogin.AccessToken))
		require.Len(t, subtree.Entries, 1)
		require.Equal(t, "public/a.txt", subtree.Entries[0].Path)

		blob := doGet(t, base+"/blob?rev=main&path=public/a.txt", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, blob.StatusCode)
		raw, err := io.ReadAll(blob.Body)
		blob.Body.Close()
		require.NoError(t, err)
		require.Equal(t, "hello", string(raw))

		commits := decodeBody[[]model.CommitResponse](t, doGet(t, base+"/commits?branch=main", aliceLogin.AccessToken))
		require.Len(t, commits, 1)
		require.Equal(t, seed.CommitID.Base36(), commits[0].ID)
		require.Equal(t, "seed", commits[0].Message)

		commitDiff := decodeBody[model.CommitDiffResponse](t,
			doGet(t, base+"/commits/"+seed.CommitID.Base36()+"/diff", aliceLogin.AccessToken))
		require.Len(t, commitDiff.Files, 2)
		require.NotEmpty(t, commitDiff.Files[0].Patch)

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		require.Len(t, branches, 1)
		require.Equal(t, "main", branches[0].Name)
		require.True(t, branches[0].IsDefault)

		reader, err := userRepo.Create(context.Background(), domain.User{
			ID: node.Generate(), Name: "reader", Email: "reader@example.com", Password: "hashed-password",
		})
		require.NoError(t, err)
		projectID := snow.ID(1)
		_, err = pbacRepo.CreateRule(context.Background(), domain.PBACRule{
			UserID: &reader.ID, OrgID: 1, ProjectID: &projectID,
			PathPrefix: "public", Permission: domain.PermissionRead,
		})
		require.NoError(t, err)

		readerLogin, _ := loginAs(t, "reader@example.com")
		readerTree := decodeBody[model.TreeResponse](t, doGet(t, base+"/tree?rev=main", readerLogin.AccessToken))
		require.Len(t, readerTree.Entries, 1)
		require.Equal(t, "public", readerTree.Entries[0].Path)

		hidden := doGet(t, base+"/blob?rev=main&path=secret/key.bin", readerLogin.AccessToken)
		require.Equal(t, http.StatusNotFound, hidden.StatusCode)
		hidden.Body.Close()
	})

	t.Run("branch management", func(t *testing.T) {
		aliceLogin, _ := login(t)
		bobLogin, _ := loginAs(t, "bob@example.com")
		base := server.URL + "/api/v1/orgs/default/projects/default/branches"

		create := doMethod(t, http.MethodPost, base, `{"name":"feature","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, create.StatusCode)
		feature := decodeBody[model.BranchResponse](t, create)
		require.Equal(t, "feature", feature.Name)

		rename := doMethod(t, http.MethodPatch, base+"/feature", `{"name":"renamed"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, rename.StatusCode)
		renamed := decodeBody[model.BranchResponse](t, rename)
		require.Equal(t, "renamed", renamed.Name)

		protect := doMethod(t, http.MethodPut, base+"/renamed/protection", `{"protected":true,"dismiss_stale_approvals":false}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, protect.StatusCode)
		protected := decodeBody[model.BranchResponse](t, protect)
		require.True(t, protected.IsProtected)
		require.False(t, protected.DismissStaleApprovals)

		denied := doMethod(t, http.MethodPost, base, `{"name":"nope"}`, bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, denied.StatusCode)
		denied.Body.Close()

		makeDefault := doMethod(t, http.MethodPost, base+"/renamed/default", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, makeDefault.StatusCode)
		require.True(t, decodeBody[model.BranchResponse](t, makeDefault).IsDefault)

		blocked := doMethod(t, http.MethodDelete, base+"/renamed", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, blocked.StatusCode)
		blocked.Body.Close()

		restore := doMethod(t, http.MethodPost, base+"/main/default", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, restore.StatusCode)
		restore.Body.Close()

		stillProtected := doMethod(t, http.MethodDelete, base+"/renamed", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, stillProtected.StatusCode)
		stillProtected.Body.Close()

		unprotect := doMethod(t, http.MethodPut, base+"/renamed/protection", `{"protected":false}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, unprotect.StatusCode)
		require.False(t, decodeBody[model.BranchResponse](t, unprotect).IsProtected)

		remove := doMethod(t, http.MethodDelete, base+"/renamed", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, remove.StatusCode)
		remove.Body.Close()

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, base, aliceLogin.AccessToken))
		require.Len(t, branches, 1)
		require.Equal(t, "main", branches[0].Name)
	})

	t.Run("tags", func(t *testing.T) {
		aliceLogin, _ := login(t)
		bobLogin, _ := loginAs(t, "bob@example.com")
		projectBase := server.URL + "/api/v1/orgs/default/projects/default"
		base := projectBase + "/tags"

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, projectBase+"/branches", aliceLogin.AccessToken))
		var mainHead string
		for _, branch := range branches {
			if branch.Name == "main" {
				mainHead = branch.CommitID
			}
		}
		require.NotEmpty(t, mainHead)

		create := doMethod(t, http.MethodPost, base, `{"name":"v1.0.0","from":"main","message":"first release"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, create.StatusCode)
		tag := decodeBody[model.TagResponse](t, create)
		require.Equal(t, "v1.0.0", tag.Name)
		require.Equal(t, mainHead, tag.CommitID)
		require.Equal(t, "first release", tag.Message)
		require.NotEmpty(t, tag.ID)

		fromCommit := doMethod(t, http.MethodPost, base, `{"name":"v1.0.1","from":"`+mainHead+`"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, fromCommit.StatusCode)
		require.Equal(t, mainHead, decodeBody[model.TagResponse](t, fromCommit).CommitID)

		got := decodeBody[model.TagResponse](t, doGet(t, base+"/v1.0.0", aliceLogin.AccessToken))
		require.Equal(t, tag.ID, got.ID)
		require.Equal(t, "v1.0.0", got.Name)

		tags := decodeBody[[]model.TagResponse](t, doGet(t, base, aliceLogin.AccessToken))
		require.Len(t, tags, 2)

		badCursor := doGet(t, base+"?last_id="+tag.ID, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badCursor.StatusCode)
		_ = badCursor.Body.Close()

		duplicate := doMethod(t, http.MethodPost, base, `{"name":"v1.0.0","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, duplicate.StatusCode)
		_ = duplicate.Body.Close()

		denied := doMethod(t, http.MethodPost, base, `{"name":"v2.0.0","from":"main"}`, bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, denied.StatusCode)
		_ = denied.Body.Close()

		remove := doMethod(t, http.MethodDelete, base+"/v1.0.0", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, remove.StatusCode)
		_ = remove.Body.Close()

		gone := doGet(t, base+"/v1.0.0", aliceLogin.AccessToken)
		require.Equal(t, http.StatusNotFound, gone.StatusCode)
		_ = gone.Body.Close()

		remaining := decodeBody[[]model.TagResponse](t, doGet(t, base, aliceLogin.AccessToken))
		require.Len(t, remaining, 1)
		require.Equal(t, "v1.0.1", remaining[0].Name)
	})

	t.Run("merge requests", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default"

		createBranch := doMethod(t, http.MethodPost, base+"/branches", `{"name":"feature","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBranch.StatusCode)
		createBranch.Body.Close()

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		var mainHead string
		for _, branch := range branches {
			if branch.Name == "main" {
				mainHead = branch.CommitID
			}
		}
		require.NotEmpty(t, mainHead)
		featurePush := seedPushTo(t, "feature", mainHead, map[string]string{"feature.txt": "feature"})

		createMR := doMethod(t, http.MethodPost, base+"/merge-requests",
			`{"title":"Add feature","description":"body","source_branch":"feature","target_branch":"main"}`,
			aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createMR.StatusCode)
		mr := decodeBody[model.MergeRequestResponse](t, createMR)
		require.Equal(t, "open", mr.Status)

		mrURL := base + "/merge-requests/" + strconv.FormatInt(mr.Number, 10)
		detail := decodeBody[model.MergeRequestResponse](t, doGet(t, mrURL, aliceLogin.AccessToken))
		require.NotNil(t, detail.Mergeability)
		require.Equal(t, domain.MergeabilityMergeable, detail.Mergeability.Status)

		updated := decodeBody[model.MergeRequestResponse](t,
			doMethod(t, http.MethodPatch, mrURL, `{"title":"Add feature v2","description":"updated"}`, aliceLogin.AccessToken))
		require.Equal(t, "Add feature v2", updated.Title)
		require.Equal(t, "updated", updated.Description)

		drafted := decodeBody[model.MergeRequestResponse](t,
			doMethod(t, http.MethodPatch, mrURL, `{"draft":true}`, aliceLogin.AccessToken))
		require.True(t, drafted.Draft)

		draftBlocked := doMethod(t, http.MethodPost, mrURL+"/merge", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, draftBlocked.StatusCode)
		draftBlocked.Body.Close()

		drafts := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?draft=true", aliceLogin.AccessToken))
		require.Len(t, drafts.MergeRequests, 1)
		require.True(t, drafts.MergeRequests[0].Draft)

		ready := decodeBody[model.MergeRequestResponse](t,
			doMethod(t, http.MethodPatch, mrURL, `{"draft":false}`, aliceLogin.AccessToken))
		require.False(t, ready.Draft)

		diff := decodeBody[model.MergeRequestDiffResponse](t, doGet(t, mrURL+"/diff", aliceLogin.AccessToken))
		require.Len(t, diff.Files, 1)
		require.Equal(t, "feature.txt", diff.Files[0].Path)

		commits := decodeBody[[]model.CommitResponse](t, doGet(t, mrURL+"/commits", aliceLogin.AccessToken))
		require.Len(t, commits, 1, "only the source-side commit belongs to the request")
		require.Equal(t, featurePush.CommitID.Base36(), commits[0].ID)
		require.Equal(t, "seed", commits[0].Message)
		require.Equal(t, "Alice Admin", commits[0].AuthorName)

		merge := doMethod(t, http.MethodPost, mrURL+"/merge", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, merge.StatusCode)
		merged := decodeBody[model.MergeRequestResponse](t, merge)
		require.Equal(t, domain.MergeRequestMerged, merged.Status)
		require.Equal(t, featurePush.CommitID.Base36(), merged.MergeCommitID)

		again := doMethod(t, http.MethodPost, mrURL+"/merge", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, again.StatusCode)
		again.Body.Close()

		// Diverged: lagging branch created before the merge, then main advances.
		createBranch = doMethod(t, http.MethodPost, base+"/branches", `{"name":"lagging","from":"feature"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBranch.StatusCode)
		createBranch.Body.Close()
		seedPushTo(t, "lagging", featurePush.CommitID.Base36(), map[string]string{"lagging.txt": "lag"})
		seedPushTo(t, "main", featurePush.CommitID.Base36(), map[string]string{"after.txt": "after"})

		createMR = doMethod(t, http.MethodPost, base+"/merge-requests",
			`{"title":"Lagging","source_branch":"lagging","target_branch":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createMR.StatusCode)
		lagging := decodeBody[model.MergeRequestResponse](t, createMR)

		laggingURL := base + "/merge-requests/" + strconv.FormatInt(lagging.Number, 10)
		check := decodeBody[*model.MergeabilityResponse](t, doGet(t, laggingURL+"/check", aliceLogin.AccessToken))
		require.Equal(t, domain.MergeabilityBehind, check.Status)

		blocked := doMethod(t, http.MethodPost, laggingURL+"/merge", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, blocked.StatusCode)
		blocked.Body.Close()

		closed := decodeBody[model.MergeRequestResponse](t,
			doMethod(t, http.MethodPost, laggingURL+"/close", "", aliceLogin.AccessToken))
		require.Equal(t, domain.MergeRequestClosed, closed.Status)
		reopened := decodeBody[model.MergeRequestResponse](t,
			doMethod(t, http.MethodPost, laggingURL+"/reopen", "", aliceLogin.AccessToken))
		require.Equal(t, "open", reopened.Status)

		list := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?status=merged", aliceLogin.AccessToken))
		require.Len(t, list.MergeRequests, 1)

		// pagination: the newest number is the keyset cursor
		firstPage := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?limit=1", aliceLogin.AccessToken))
		require.Len(t, firstPage.MergeRequests, 1)
		require.Equal(t, lagging.Number, firstPage.MergeRequests[0].Number)
		require.Equal(t, strconv.FormatInt(lagging.Number, 10), firstPage.NextCursor)
		secondPage := decodeBody[model.MergeRequestListResponse](t,
			doGet(t, base+"/merge-requests?limit=1&after="+firstPage.NextCursor, aliceLogin.AccessToken))
		require.Len(t, secondPage.MergeRequests, 1)
		require.Equal(t, mr.Number, secondPage.MergeRequests[0].Number)

		// filters narrow the page
		byAuthor := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?author="+mr.CreatedBy, aliceLogin.AccessToken))
		require.Len(t, byAuthor.MergeRequests, 2)
		bySource := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?source=lagging", aliceLogin.AccessToken))
		require.Len(t, bySource.MergeRequests, 1)
		require.Equal(t, lagging.Number, bySource.MergeRequests[0].Number)
		byTarget := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?target=ghost", aliceLogin.AccessToken))
		require.Empty(t, byTarget.MergeRequests)

		badAfter := doMethod(t, http.MethodGet, base+"/merge-requests?after=nope", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badAfter.StatusCode)
		badAfter.Body.Close()

		badAuthor := doMethod(t, http.MethodGet, base+"/merge-requests?author=!!", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badAuthor.StatusCode)
		badAuthor.Body.Close()

		// a non-positive limit falls back to the default page size
		zeroLimit := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests?limit=0", aliceLogin.AccessToken))
		require.Len(t, zeroLimit.MergeRequests, 2)

		// Protected targets can only be moved by merging a merge request.
		createBranch = doMethod(t, http.MethodPost, base+"/branches", `{"name":"protected-fix","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBranch.StatusCode)
		createBranch.Body.Close()
		branches = decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		var currentMain string
		for _, branch := range branches {
			if branch.Name == "main" {
				currentMain = branch.CommitID
			}
		}
		require.NotEmpty(t, currentMain)
		seedPushTo(t, "protected-fix", currentMain, map[string]string{"fix.txt": "fix"})

		protect := doMethod(t, http.MethodPut, base+"/branches/main/protection", `{"protected":true}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, protect.StatusCode)
		protect.Body.Close()

		createMR = doMethod(t, http.MethodPost, base+"/merge-requests",
			`{"title":"Fix","source_branch":"protected-fix","target_branch":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createMR.StatusCode)
		fixMR := decodeBody[model.MergeRequestResponse](t, createMR)

		merge = doMethod(t, http.MethodPost, base+"/merge-requests/"+strconv.FormatInt(fixMR.Number, 10)+"/merge", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, merge.StatusCode)
		require.Equal(t, domain.MergeRequestMerged, decodeBody[model.MergeRequestResponse](t, merge).Status)

		unprotect := doMethod(t, http.MethodPut, base+"/branches/main/protection", `{"protected":false}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, unprotect.StatusCode)
		unprotect.Body.Close()
	})

	t.Run("project ACL admin", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default/permissions"

		createViewer := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"viewer","email":"acl-viewer@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createViewer.StatusCode)
		viewer := decodeBody[model.UserResponse](t, createViewer)
		viewerLogin, _ := loginAs(t, "acl-viewer@example.com")

		createUser := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"grantee","email":"grantee@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createUser.StatusCode)
		grantee := decodeBody[model.UserResponse](t, createUser)
		_ = viewer
		t.Logf("grantee id=%q email=%q", grantee.ID, grantee.Email)

		createRule := doMethod(t, http.MethodPost, base+"/rules",
			`{"user_id":"`+grantee.ID+`","path_prefix":"public","permission":1}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createRule.StatusCode)
		rule := decodeBody[model.PBACRuleResponse](t, createRule)
		require.NotZero(t, rule.ID)

		rules := decodeBody[[]model.PBACRuleResponse](t, doGet(t, base+"/rules", aliceLogin.AccessToken))
		require.Len(t, rules, 2, "reader rule from the repository browser plus the grantee rule")
		var granteeRule *model.PBACRuleResponse
		for i := range rules {
			if rules[i].ID == rule.ID {
				granteeRule = &rules[i]
			}
		}
		require.NotNil(t, granteeRule)
		require.Equal(t, grantee.ID, granteeRule.UserID)
		require.Equal(t, "grantee", granteeRule.UserName)
		require.Equal(t, "grantee@example.com", granteeRule.UserEmail)

		granteeLogin, _ := loginAs(t, "grantee@example.com")
		info := decodeBody[model.ProjectPermissionResponse](t, doGet(t, base+"/me", granteeLogin.AccessToken))
		require.Equal(t, uint64(1), info.ProjectPermission)
		require.Len(t, info.Rules, 1)

		denied := doMethod(t, http.MethodPost, base+"/rules", `{"user_id":"1","permission":1}`, viewerLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, denied.StatusCode)
		denied.Body.Close()

		badRule := doMethod(t, http.MethodPost, base+"/rules", `{"user_id":"1","permission":1048576}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badRule.StatusCode)
		badRule.Body.Close()

		badDefault := doMethod(t, http.MethodPut, base+"/defaults", `{"path_prefix":"docs","permission":1048576}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badDefault.StatusCode)
		badDefault.Body.Close()

		setDefault := doMethod(t, http.MethodPut, base+"/defaults", `{"path_prefix":"docs","permission":1}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, setDefault.StatusCode)
		setDefault.Body.Close()

		defaults := decodeBody[[]model.PermissionEntry](t, doGet(t, base+"/defaults", aliceLogin.AccessToken))
		require.Len(t, defaults, 1)

		removeDefault := doMethod(t, http.MethodDelete, base+"/defaults?path=docs", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, removeDefault.StatusCode)
		removeDefault.Body.Close()

		removeRule := doMethod(t, http.MethodDelete, base+"/rules/"+strconv.FormatInt(rule.ID, 10), "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, removeRule.StatusCode)
		removeRule.Body.Close()

		rules = decodeBody[[]model.PBACRuleResponse](t, doGet(t, base+"/rules", aliceLogin.AccessToken))
		require.Len(t, rules, 1)
	})

	t.Run("webhooks admin", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default/webhooks"

		created := doMethod(t, http.MethodPost, base,
			`{"name":"ci","url":"https://example.com/hook","events":["push","mr.merged"],"path_prefix":"assets"}`,
			aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, created.StatusCode)
		webhook := decodeBody[model.WebhookResponse](t, created)
		require.NotEmpty(t, webhook.ID)
		require.Equal(t, "ci", webhook.Name)
		require.Equal(t, []string{domain.WebhookEventMRMerged, domain.WebhookEventPush}, webhook.Events)
		require.Equal(t, "assets", webhook.PathPrefix)
		require.True(t, webhook.IsActive)
		require.Len(t, webhook.Secret, 64)

		list := decodeBody[[]model.WebhookResponse](t, doGet(t, base, aliceLogin.AccessToken))
		require.Len(t, list, 1)
		require.Empty(t, list[0].Secret, "list never exposes the secret")

		updated := doMethod(t, http.MethodPatch, base+"/"+webhook.ID, `{"is_active":false}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, updated.StatusCode)
		updatedWebhook := decodeBody[model.WebhookResponse](t, updated)
		require.False(t, updatedWebhook.IsActive)
		require.Equal(t, webhook.URL, updatedWebhook.URL)

		rotated := doMethod(t, http.MethodPost, base+"/"+webhook.ID+"/rotate-secret", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, rotated.StatusCode)
		rotatedWebhook := decodeBody[model.WebhookResponse](t, rotated)
		require.Len(t, rotatedWebhook.Secret, 64)
		require.NotEqual(t, webhook.Secret, rotatedWebhook.Secret)

		badURL := doMethod(t, http.MethodPost, base, `{"url":"not-a-url","events":["push"]}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badURL.StatusCode)
		badURL.Body.Close()

		testDelivery := doMethod(t, http.MethodPost, base+"/"+webhook.ID+"/test", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, testDelivery.StatusCode)
		delivery := decodeBody[model.WebhookDeliveryResponse](t, testDelivery)
		require.NotEmpty(t, delivery.ID)
		require.Equal(t, domain.WebhookEventPing, delivery.EventType)
		require.Equal(t, domain.WebhookDeliveryPending, delivery.State)

		deliveries := decodeBody[[]model.WebhookDeliveryResponse](t, doGet(t, base+"/"+webhook.ID+"/deliveries", aliceLogin.AccessToken))
		require.Len(t, deliveries, 1)
		require.Equal(t, delivery.ID, deliveries[0].ID)

		redeliver := doMethod(t, http.MethodPost, base+"/"+webhook.ID+"/deliveries/"+delivery.ID+"/redeliver", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, redeliver.StatusCode)
		require.Equal(t, delivery.ID, decodeBody[model.WebhookDeliveryResponse](t, redeliver).ID)

		badLimit := doGet(t, base+"/"+webhook.ID+"/deliveries?limit=nope", aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badLimit.StatusCode)
		badLimit.Body.Close()

		deleted := doMethod(t, http.MethodDelete, base+"/"+webhook.ID, "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, deleted.StatusCode)
		deleted.Body.Close()

		missing := doMethod(t, http.MethodGet, base+"/"+webhook.ID, "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusNotFound, missing.StatusCode)
		missing.Body.Close()
	})

	t.Run("email deliveries admin", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default/emails/deliveries"

		list := decodeBody[model.EmailDeliveryListResponse](t, doGet(t, base, aliceLogin.AccessToken))
		require.Empty(t, list.Deliveries)
		require.Empty(t, list.NextCursor)

		invalidState := doGet(t, base+"?state=bogus", aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, invalidState.StatusCode)
		invalidState.Body.Close()

		invalidAfter := doGet(t, base+"?after=!!!", aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, invalidAfter.StatusCode)
		invalidAfter.Body.Close()

		invalidLimit := doGet(t, base+"?limit=abc", aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, invalidLimit.StatusCode)
		invalidLimit.Body.Close()

		missing := doMethod(t, http.MethodPost, base+"/1/redeliver", "{}", aliceLogin.AccessToken)
		require.Equal(t, http.StatusNotFound, missing.StatusCode)
		missing.Body.Close()

		bobLogin, _ := loginAs(t, "bob@example.com")
		forbidden := doGet(t, base, bobLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, forbidden.StatusCode)
		forbidden.Body.Close()
	})

	t.Run("webhook delivery", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default"

		type received struct {
			event     string
			body      []byte
			signature string
		}
		requests := make(chan received, 10)
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			requests <- received{
				event:     r.Header.Get(webhook.EventHeader),
				body:      body,
				signature: r.Header.Get(webhook.SignatureHeader),
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(receiver.Close)

		created := doMethod(t, http.MethodPost, base+"/webhooks",
			`{"name":"ci","url":"`+receiver.URL+`","events":["push","branch.created","mr.synchronized"]}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, created.StatusCode)
		hook := decodeBody[model.WebhookResponse](t, created)
		t.Cleanup(func() {
			resp := doMethod(t, http.MethodDelete, base+"/webhooks/"+hook.ID, "", aliceLogin.AccessToken)
			resp.Body.Close()
		})

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		var mainHead string
		for _, branch := range branches {
			if branch.Name == "main" {
				mainHead = branch.CommitID
			}
		}
		require.NotEmpty(t, mainHead)
		seedPushTo(t, "main", mainHead, map[string]string{"hook/note.txt": "hello"})

		createBranch := doMethod(t, http.MethodPost, base+"/branches", `{"name":"hook-branch","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBranch.StatusCode)
		createBranch.Body.Close()

		var inbox []received
		waitForEvent := func(event string) received {
			t.Helper()
			timeout := time.After(5 * time.Second)
			for {
				for _, delivery := range inbox {
					if delivery.event == event {
						return delivery
					}
				}
				select {
				case delivery := <-requests:
					inbox = append(inbox, delivery)
				case <-timeout:
					t.Fatalf("timed out waiting for %q delivery, got %v", event, inbox)
				}
			}
		}
		pushDelivery := waitForEvent(domain.WebhookEventPush)
		branchDelivery := waitForEvent(domain.WebhookEventBranchCreated)

		require.NotEmpty(t, pushDelivery.event)
		require.True(t, webhook.Verify(hook.Secret, pushDelivery.body, pushDelivery.signature))
		var pushPayload webhook.PushPayload
		require.NoError(t, json.Unmarshal(pushDelivery.body, &pushPayload))
		require.Equal(t, hook.ID, pushPayload.WebhookID)
		require.Equal(t, "default", pushPayload.Organization.Slug)
		require.Equal(t, "default", pushPayload.Project.Slug)
		require.Len(t, pushPayload.Changes, 1)
		require.Equal(t, "main", pushPayload.Changes[0].Branch)
		require.NotEmpty(t, pushPayload.Changes[0].After)
		require.Contains(t, pushPayload.Changes[0].Files, webhook.PushFile{
			Path: "hook/note.txt", Operation: webhook.PushFileAdded, SizeBytes: 5,
		})

		require.NotEmpty(t, branchDelivery.event)
		require.True(t, webhook.Verify(hook.Secret, branchDelivery.body, branchDelivery.signature))
		var branchPayload webhook.BranchPayload
		require.NoError(t, json.Unmarshal(branchDelivery.body, &branchPayload))
		require.Equal(t, "hook-branch", branchPayload.Branch.Name)
		require.NotEmpty(t, branchPayload.Branch.CommitID)

		// a push to an open merge request's source branch synchronizes it
		createMR := doMethod(t, http.MethodPost, base+"/merge-requests",
			`{"title":"Sync","source_branch":"hook-branch","target_branch":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createMR.StatusCode)
		mergeRequest := decodeBody[model.MergeRequestResponse](t, createMR)

		branches = decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		var syncHead string
		for _, branch := range branches {
			if branch.Name == "hook-branch" {
				syncHead = branch.CommitID
			}
		}
		require.NotEmpty(t, syncHead)
		seedPushTo(t, "hook-branch", syncHead, map[string]string{"hook/sync.txt": "sync"})
		syncDelivery := waitForEvent(domain.WebhookEventMRSynchronized)
		require.NotEmpty(t, syncDelivery.event, "expected an mr.synchronized delivery, got %v", inbox)
		require.True(t, webhook.Verify(hook.Secret, syncDelivery.body, syncDelivery.signature))
		var syncPayload webhook.MergeRequestPayload
		require.NoError(t, json.Unmarshal(syncDelivery.body, &syncPayload))
		require.Equal(t, hook.ID, syncPayload.WebhookID)
		require.Equal(t, mergeRequest.Number, syncPayload.MergeRequest.Number)
		require.Equal(t, "hook-branch", syncPayload.MergeRequest.SourceBranch)
		require.Equal(t, domain.MergeRequestOpen, syncPayload.MergeRequest.State)

		listed := decodeBody[[]model.WebhookDeliveryResponse](t, doGet(t, base+"/webhooks/"+hook.ID+"/deliveries", aliceLogin.AccessToken))
		require.Len(t, listed, 4)
	})

	t.Run("merge request reviews", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default"

		// a second user with project write access, so someone else can review
		createReviewer := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"rev","email":"mr-reviewer@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createReviewer.StatusCode)
		reviewer := decodeBody[model.UserResponse](t, createReviewer)
		reviewerLogin, _ := loginAs(t, "mr-reviewer@example.com")
		grant := doMethod(t, http.MethodPost, base+"/permissions/rules",
			`{"user_id":"`+reviewer.ID+`","path_prefix":"","permission":`+
				strconv.FormatUint(uint64(domain.PermissionRead|domain.PermissionWrite), 10)+`}`,
			aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, grant.StatusCode)
		grant.Body.Close()

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		var mainHead string
		for _, branch := range branches {
			if branch.Name == "main" {
				mainHead = branch.CommitID
			}
		}
		createBranch := doMethod(t, http.MethodPost, base+"/branches", `{"name":"reviewed","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBranch.StatusCode)
		createBranch.Body.Close()
		head := seedPushTo(t, "reviewed", mainHead, map[string]string{"code.txt": "one\ntwo\nthree\n"})

		createMR := doMethod(t, http.MethodPost, base+"/merge-requests",
			`{"title":"Reviewed change","source_branch":"reviewed","target_branch":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createMR.StatusCode)
		mr := decodeBody[model.MergeRequestResponse](t, createMR)
		mrURL := base + "/merge-requests/" + strconv.FormatInt(mr.Number, 10)
		require.Nil(t, mr.Review, "a fresh merge request has no live review decision")

		// requesting a review
		requestReview := doMethod(t, http.MethodPost, mrURL+"/review-requests",
			`{"user_id":"`+reviewer.ID+`"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, requestReview.StatusCode)
		pending := decodeBody[model.ReviewRequestResponse](t, requestReview)
		require.Equal(t, reviewer.ID, pending.Reviewer.UserID)

		state := decodeBody[model.ReviewStateResponse](t, doGet(t, mrURL+"/review-state", aliceLogin.AccessToken))
		require.Equal(t, []string{reviewer.ID}, state.OutstandingReviewers)
		require.Zero(t, state.Approvals)

		// the author cannot approve their own change
		selfApprove := doMethod(t, http.MethodPost, mrURL+"/reviews", `{"state":"approved","body":"self"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, selfApprove.StatusCode)
		selfApprove.Body.Close()

		// a decision with an inline comment and a top-level comment
		submit := doMethod(t, http.MethodPost, mrURL+"/reviews",
			`{"state":"changes_requested","body":"please fix","comments":[`+
				`{"file_path":"code.txt","new_line":2,"body":"rename this"},`+
				`{"body":"overall"}]}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, submit.StatusCode)
		review := decodeBody[model.ReviewResponse](t, submit)
		require.Equal(t, "changes_requested", review.State)
		require.False(t, review.Stale)
		require.Equal(t, head.CommitID.Base36(), review.HeadCommitID)

		state = decodeBody[model.ReviewStateResponse](t, doGet(t, mrURL+"/review-state", aliceLogin.AccessToken))
		require.Equal(t, 1, state.ChangesRequested)
		require.Empty(t, state.OutstandingReviewers, "reviewing answers the request")
		require.Empty(t, decodeBody[[]model.ReviewRequestResponse](t, doGet(t, mrURL+"/review-requests", aliceLogin.AccessToken)))

		threads := decodeBody[[]model.ThreadResponse](t, doGet(t, mrURL+"/threads", aliceLogin.AccessToken))
		require.Len(t, threads, 2)
		byPath := map[string]model.ThreadResponse{}
		for _, thread := range threads {
			byPath[thread.FilePath] = thread
			require.Len(t, thread.Comments, 1)
			require.Equal(t, "right", thread.Side)
			require.False(t, thread.Outdated)
		}
		inline := byPath["code.txt"]
		require.Equal(t, 2, *inline.NewLine)
		require.Equal(t, "rename this", inline.Comments[0].Body)
		require.Equal(t, reviewer.ID, inline.Comments[0].User.UserID)
		require.NotEmpty(t, byPath[""].CreatedBy.UserID)

		// replying, resolving and reopening a thread
		reply := doMethod(t, http.MethodPost, mrURL+"/threads/"+inline.ID+"/comments",
			`{"body":"done, renamed"}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, reply.StatusCode)
		require.Equal(t, "done, renamed", decodeBody[model.CommentResponse](t, reply).Body)

		resolve := doMethod(t, http.MethodPost, mrURL+"/threads/"+inline.ID+"/resolve",
			`{"resolved":true}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, resolve.StatusCode)
		require.True(t, decodeBody[model.ThreadResponse](t, resolve).Resolved)

		unresolved := decodeBody[[]model.ThreadResponse](t, doGet(t, mrURL+"/threads?resolved=false", aliceLogin.AccessToken))
		require.Len(t, unresolved, 1)
		require.Equal(t, byPath[""].ID, unresolved[0].ID)

		// a comment anchored to a line the diff does not show is rejected
		badAnchor := doMethod(t, http.MethodPost, mrURL+"/threads",
			`{"file_path":"code.txt","new_line":99,"body":"nowhere"}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusBadRequest, badAnchor.StatusCode)
		badAnchor.Body.Close()

		// a new push to the source branch makes the decision stale
		seedPushTo(t, "reviewed", head.CommitID.Base36(), map[string]string{"code.txt": "one\nTWO\nthree\n"})

		reviews := decodeBody[[]model.ReviewResponse](t, doGet(t, mrURL+"/reviews", aliceLogin.AccessToken))
		require.Len(t, reviews, 1)
		require.True(t, reviews[0].Stale)
		require.NotNil(t, reviews[0].DismissedAt)
		require.Equal(t, domain.MergeRequestDismissedNewCommits, reviews[0].DismissedReason)

		state = decodeBody[model.ReviewStateResponse](t, doGet(t, mrURL+"/review-state", aliceLogin.AccessToken))
		require.Zero(t, state.ChangesRequested, "a dismissed decision stops counting")
		require.Zero(t, state.DismissedApprovals, "only dismissed approvals are reported")

		// the outdated comment thread is flagged but kept
		threads = decodeBody[[]model.ThreadResponse](t, doGet(t, mrURL+"/threads", aliceLogin.AccessToken))
		require.Len(t, threads, 2)
		require.True(t, byPath["code.txt"].ID == threads[0].ID || byPath["code.txt"].ID == threads[1].ID)
		outdated := 0
		for _, thread := range threads {
			if thread.Outdated {
				outdated++
			}
		}
		require.Equal(t, 1, outdated)

		// the timeline records the request, the review and the pushes
		timeline := decodeBody[[]model.TimelineItemResponse](t, doGet(t, mrURL+"/timeline", aliceLogin.AccessToken))
		kinds := make([]string, 0, len(timeline))
		for _, item := range timeline {
			kinds = append(kinds, item.Kind)
			if item.Kind == domain.MergeRequestEventReviewRequested {
				require.NotNil(t, item.Subject)
				require.Equal(t, reviewer.ID, item.Subject.UserID)
			}
		}
		require.Equal(t, []string{
			domain.MergeRequestEventOpened,
			domain.MergeRequestEventReviewRequested,
			domain.MergeRequestEventReviewSubmitted,
			domain.MergeRequestEventPushed,
		}, kinds)

		// re-reviewing for the new head re-approves, the reviewer may withdraw
		reReview := doMethod(t, http.MethodPost, mrURL+"/reviews",
			`{"state":"approved","body":"looks good now"}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, reReview.StatusCode)
		approval := decodeBody[model.ReviewResponse](t, reReview)
		require.Equal(t, "approved", approval.State)
		require.False(t, approval.Stale)

		state = decodeBody[model.ReviewStateResponse](t, doGet(t, mrURL+"/review-state", aliceLogin.AccessToken))
		require.Equal(t, 1, state.Approvals)

		// the merge request list carries the review summary of every request
		// that still has a live decision
		list := decodeBody[model.MergeRequestListResponse](t, doGet(t, base+"/merge-requests", aliceLogin.AccessToken))
		var listed *model.MergeRequestResponse
		for i := range list.MergeRequests {
			if list.MergeRequests[i].Number == mr.Number {
				listed = &list.MergeRequests[i]
			}
		}
		require.NotNil(t, listed)
		require.NotNil(t, listed.Review)
		require.Equal(t, 1, listed.Review.Approvals)

		// write access is not enough to withdraw somebody else's review
		createPeer := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"peer","email":"mr-peer@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createPeer.StatusCode)
		peer := decodeBody[model.UserResponse](t, createPeer)
		peerLogin, _ := loginAs(t, "mr-peer@example.com")
		peerGrant := doMethod(t, http.MethodPost, base+"/permissions/rules",
			`{"user_id":"`+peer.ID+`","path_prefix":"","permission":`+
				strconv.FormatUint(uint64(domain.PermissionRead|domain.PermissionWrite), 10)+`}`,
			aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, peerGrant.StatusCode)
		peerGrant.Body.Close()

		notYours := doMethod(t, http.MethodDelete, mrURL+"/reviews/"+approval.ID, "", peerLogin.AccessToken)
		require.Equal(t, http.StatusForbidden, notYours.StatusCode)
		notYours.Body.Close()

		withdraw := doMethod(t, http.MethodDelete, mrURL+"/reviews/"+approval.ID, "", reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, withdraw.StatusCode)
		withdraw.Body.Close()
		state = decodeBody[model.ReviewStateResponse](t, doGet(t, mrURL+"/review-state", aliceLogin.AccessToken))
		require.Zero(t, state.Approvals)

		// the merge request author can dismiss a review instead of deleting it,
		// which keeps it in the history
		again := doMethod(t, http.MethodPost, mrURL+"/reviews",
			`{"state":"approved","body":"still fine"}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, again.StatusCode)
		last := decodeBody[model.ReviewResponse](t, again)

		dismiss := doMethod(t, http.MethodPost, mrURL+"/reviews/"+last.ID+"/dismiss", `{}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, dismiss.StatusCode)
		require.Equal(t, "manual", decodeBody[model.ReviewResponse](t, dismiss).DismissedReason)
		state = decodeBody[model.ReviewStateResponse](t, doGet(t, mrURL+"/review-state", aliceLogin.AccessToken))
		require.Zero(t, state.Approvals)
		require.Equal(t, 1, state.DismissedApprovals)

		// handler error paths: unknown merge request, unparsable ids
		missing := base + "/merge-requests/424242"
		for _, tc := range []struct {
			method string
			url    string
			body   string
			want   int
		}{
			{http.MethodGet, missing + "/reviews", "", http.StatusNotFound},
			{http.MethodPost, missing + "/reviews", `{"state":"commented","body":"x"}`, http.StatusNotFound},
			{http.MethodGet, missing + "/review-state", "", http.StatusNotFound},
			{http.MethodGet, missing + "/threads", "", http.StatusNotFound},
			{http.MethodPost, missing + "/threads", `{"body":"x"}`, http.StatusNotFound},
			{http.MethodGet, missing + "/review-requests", "", http.StatusNotFound},
			{http.MethodPost, missing + "/review-requests", `{"user_id":"1"}`, http.StatusNotFound},
			{http.MethodDelete, missing + "/review-requests", `{"user_id":"1"}`, http.StatusNotFound},
			{http.MethodGet, missing + "/timeline", "", http.StatusNotFound},
			{http.MethodGet, missing + "/commits", "", http.StatusNotFound},
			{http.MethodDelete, mrURL + "/reviews/not-base36", "", http.StatusBadRequest},
			{http.MethodPost, mrURL + "/reviews/not-base36/dismiss", "", http.StatusBadRequest},
			{http.MethodPost, mrURL + "/threads/not-base36/comments", `{"body":"x"}`, http.StatusBadRequest},
			{http.MethodPatch, mrURL + "/threads/not-base36/comments/not-base36", `{"body":"x"}`, http.StatusBadRequest},
			{http.MethodDelete, mrURL + "/threads/not-base36/comments/not-base36", "", http.StatusBadRequest},
			{http.MethodPost, mrURL + "/threads/not-base36/resolve", `{"resolved":true}`, http.StatusBadRequest},
			{http.MethodDelete, mrURL + "/threads/not-base36", "", http.StatusBadRequest},
		} {
			resp := doMethod(t, tc.method, tc.url, tc.body, aliceLogin.AccessToken)
			require.Equal(t, tc.want, resp.StatusCode, "%s %s", tc.method, tc.url)
			resp.Body.Close()
		}

		unauthenticated := doMethod(t, http.MethodGet, mrURL+"/reviews", "", "")
		require.Equal(t, http.StatusUnauthorized, unauthenticated.StatusCode)
		unauthenticated.Body.Close()
	})

	t.Run("merge request approval gate", func(t *testing.T) {
		aliceLogin, _ := login(t)
		base := server.URL + "/api/v1/orgs/default/projects/default"

		createReviewer := doMethod(t, http.MethodPost, server.URL+"/api/v1/users",
			`{"name":"gate","email":"mr-gate@example.com","password":"password123"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createReviewer.StatusCode)
		reviewer := decodeBody[model.UserResponse](t, createReviewer)
		reviewerLogin, _ := loginAs(t, "mr-gate@example.com")
		grant := doMethod(t, http.MethodPost, base+"/permissions/rules",
			`{"user_id":"`+reviewer.ID+`","path_prefix":"","permission":`+
				strconv.FormatUint(uint64(domain.PermissionRead|domain.PermissionWrite), 10)+`}`,
			aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, grant.StatusCode)
		grant.Body.Close()

		branches := decodeBody[[]model.BranchResponse](t, doGet(t, base+"/branches", aliceLogin.AccessToken))
		var mainHead string
		for _, branch := range branches {
			if branch.Name == "main" {
				mainHead = branch.CommitID
			}
		}
		require.NotEmpty(t, mainHead)
		createBranch := doMethod(t, http.MethodPost, base+"/branches", `{"name":"gated","from":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createBranch.StatusCode)
		createBranch.Body.Close()
		seedPushTo(t, "gated", mainHead, map[string]string{"gated.txt": "gated content\n"})

		createMR := doMethod(t, http.MethodPost, base+"/merge-requests",
			`{"title":"Gated change","source_branch":"gated","target_branch":"main"}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, createMR.StatusCode)
		mr := decodeBody[model.MergeRequestResponse](t, createMR)
		mrURL := base + "/merge-requests/" + strconv.FormatInt(mr.Number, 10)

		protect := doMethod(t, http.MethodPut, base+"/branches/main/protection",
			`{"protected":true,"required_approvals":1}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, protect.StatusCode)
		require.EqualValues(t, 1, decodeBody[model.BranchResponse](t, protect).RequiredApprovals)

		// the target branch requires an approval the request does not have yet
		check := decodeBody[model.MergeabilityResponse](t, doGet(t, mrURL+"/check", aliceLogin.AccessToken))
		require.Equal(t, domain.MergeabilityMergeable, check.Status)
		require.Equal(t, domain.MergeabilityBlockedApprovals, check.BlockedBy)

		blocked := doMethod(t, http.MethodPost, mrURL+"/merge", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusConflict, blocked.StatusCode)
		blocked.Body.Close()

		// a live objection blocks even when the approval count is met
		object := doMethod(t, http.MethodPost, mrURL+"/reviews",
			`{"state":"changes_requested","body":"not yet"}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, object.StatusCode)
		object.Body.Close()
		check = decodeBody[model.MergeabilityResponse](t, doGet(t, mrURL+"/check", aliceLogin.AccessToken))
		require.Equal(t, domain.MergeabilityBlockedChangesRequested, check.BlockedBy)

		// re-reviewing for the same head replaces the decision
		approve := doMethod(t, http.MethodPost, mrURL+"/reviews",
			`{"state":"approved","body":"looks good"}`, reviewerLogin.AccessToken)
		require.Equal(t, http.StatusOK, approve.StatusCode)
		approve.Body.Close()
		check = decodeBody[model.MergeabilityResponse](t, doGet(t, mrURL+"/check", aliceLogin.AccessToken))
		require.Empty(t, check.BlockedBy)

		merge := doMethod(t, http.MethodPost, mrURL+"/merge", "", aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, merge.StatusCode)
		merged := decodeBody[model.MergeRequestResponse](t, merge)
		require.Equal(t, domain.MergeRequestMerged, merged.Status)

		// the timeline records the whole lifecycle
		timeline := decodeBody[[]model.TimelineItemResponse](t, doGet(t, mrURL+"/timeline", aliceLogin.AccessToken))
		kinds := make([]string, 0, len(timeline))
		for _, item := range timeline {
			kinds = append(kinds, item.Kind)
		}
		require.Contains(t, kinds, domain.MergeRequestEventOpened)
		require.Contains(t, kinds, domain.MergeRequestEventReviewSubmitted)
		require.Contains(t, kinds, domain.MergeRequestEventMerged)

		// cleanup: the next subtests must not inherit the protection policy
		unprotect := doMethod(t, http.MethodPut, base+"/branches/main/protection",
			`{"protected":false,"required_approvals":0}`, aliceLogin.AccessToken)
		require.Equal(t, http.StatusOK, unprotect.StatusCode)
		unprotect.Body.Close()
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

func doMethodCookies(t *testing.T, method, url, body string, cookies []*http.Cookie) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
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
