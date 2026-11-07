package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/serverapp"
)

func auditREST(t *testing.T, method, url, body, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "audit-e2e/1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func auditLogin(t *testing.T, base, email, password string) string {
	t.Helper()
	resp := auditREST(t, http.MethodPost, base+"/api/v1/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var login model.LoginResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&login))
	return login.AccessToken
}

// The REST container is process-global, so this is the only e2e test that
// mounts it.
func TestEndToEnd_AuditLog(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host, uc := startTestServerWithUsecases(t, dbConn)
	server := httptest.NewServer(api.NewAPI(serverapp.NewRegistry(uc)).SetupRoute())
	t.Cleanup(server.Close)

	superToken := auditLogin(t, server.URL, e2eSuperAdminEmail, e2eSuperAdminPass)
	created := auditREST(t, http.MethodPost, server.URL+"/api/v1/users",
		`{"name":"carol","email":"carol@example.com","password":"password123"}`, superToken)
	require.Equal(t, http.StatusOK, created.StatusCode)
	_ = created.Body.Close()

	orgBase := server.URL + "/api/v1/orgs/" + e2eOrgSlug
	added := auditREST(t, http.MethodPost, orgBase+"/members", `{"email":"carol@example.com","role":"member"}`, superToken)
	require.Equal(t, http.StatusOK, added.StatusCode)
	_ = added.Body.Close()

	protect := auditREST(t, http.MethodPut, orgBase+"/projects/"+e2eProjectSlug+"/branches/main/protection",
		`{"protected":true,"required_approvals":1}`, superToken)
	require.Equal(t, http.StatusOK, protect.StatusCode)
	_ = protect.Body.Close()

	seedOtherUserLock(t, dbConn, "tex.png")
	grpcClient, _ := newLoggedInClient(t, host)
	require.NoError(t, grpcClient.UnlockFile(ctx, e2eOrgSlug, e2eProjectSlug, "tex.png", ""))

	resp := auditREST(t, http.MethodGet, server.URL+"/api/v1/admin/audit?limit=200", "", superToken)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var page model.AuditListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	_ = resp.Body.Close()

	byAction := map[string]model.AuditEntryResponse{}
	for _, entry := range page.Entries {
		byAction[entry.Action] = entry
	}
	for _, action := range []string{
		serverDomain.AuditUserCreated, serverDomain.AuditOrgMemberAdded,
		serverDomain.AuditBranchProtectionChanged, serverDomain.AuditLockForceReleased,
	} {
		entry, ok := byAction[action]
		require.True(t, ok, "missing %s", action)
		require.Equal(t, "user", entry.ActorType)
		require.NotEmpty(t, entry.ActorUserID)
		require.Equal(t, byAction[serverDomain.AuditOrgMemberAdded].ActorUserID, entry.ActorUserID, "all actions were taken by the super admin")
	}
	require.Equal(t, "audit-e2e/1", byAction[serverDomain.AuditOrgMemberAdded].UserAgent)
	require.Equal(t, "127.0.0.1", byAction[serverDomain.AuditOrgMemberAdded].ClientIP)
	unlock := byAction[serverDomain.AuditLockForceReleased]
	require.Equal(t, "tex.png", unlock.Metadata["path"])
	require.NotEmpty(t, unlock.OrgID)
	require.NotEmpty(t, unlock.ClientIP, "gRPC calls carry request meta too")

	carolToken := auditLogin(t, server.URL, "carol@example.com", "password123")
	denied := auditREST(t, http.MethodGet, server.URL+"/api/v1/admin/audit", "", carolToken)
	require.Equal(t, http.StatusForbidden, denied.StatusCode)
	_ = denied.Body.Close()
}
