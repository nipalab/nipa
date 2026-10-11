package storageusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type stubTokenValidator struct {
	claims map[string]domain.Claims
}

func (v *stubTokenValidator) ValidateToken(_ context.Context, token string) (*domain.Claims, error) {
	claims, ok := v.claims[token]
	if !ok {
		return nil, errors.New("invalid token")
	}
	return &claims, nil
}

type apiHarness struct {
	t        *testing.T
	db       *sql.DB
	ledger   *Ledger
	server   *httptest.Server
	validate *stubTokenValidator
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	db := newTestDB(t)
	ledger := NewLedger(db, Config{})

	node, err := snow.NewNode(0)
	require.NoError(t, err)
	orgRepo := postgres.NewOrgRepository(db)
	commonUc := usecase.NewCommon(orgRepo, postgres.NewProjectRepository(db))
	orgUc := usecase.NewOrg(orgRepo, node)

	validate := &stubTokenValidator{claims: map[string]domain.Claims{
		"super": {UserID: 1, IsSuperAdmin: true},
		"user":  {UserID: 2},
	}}
	ws := new(restful.WebService).ApiVersion("1.0.0")
	ws.Path("/api/v1").
		Filter(api.NewAuthFilter(validate).Auth()).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	RegisterRoutes(ws, RoutesConfig{Ledger: ledger, Common: commonUc, Orgs: orgUc})

	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)

	return &apiHarness{t: t, db: db, ledger: ledger, server: server, validate: validate}
}

func (h *apiHarness) do(method, path, token string, body any) (int, []byte) {
	h.t.Helper()
	var raw string
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(h.t, err)
		raw = string(data)
	} else if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		raw = "{}"
	}
	return h.doRaw(method, path, token, raw)
}

func (h *apiHarness) doRaw(method, path, token, raw string) (int, []byte) {
	h.t.Helper()
	var reader io.Reader
	if raw != "" {
		reader = strings.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	require.NoError(h.t, err)
	if raw != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	return resp.StatusCode, data
}

func (h *apiHarness) decode(status int, body []byte, out any) {
	h.t.Helper()
	require.Equal(h.t, http.StatusOK, status, "body: %s", body)
	require.NoError(h.t, json.Unmarshal(body, out))
}

func seedAPIUser(t *testing.T, db *sql.DB, name string) snow.ID {
	t.Helper()
	id := snow.ID(testIDCounter.Add(1) + 50000)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, name, email, password) VALUES ($1, $2, $3, 'x')`,
		id.Int64(), name, name+"-api@example.com",
	)
	require.NoError(t, err)
	return id
}

func seedAPIOrgMember(t *testing.T, db *sql.DB, orgID, userID snow.ID, role string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)`,
		orgID.Int64(), userID.Int64(), role,
	)
	require.NoError(t, err)
}

func seedAPIChunk(t *testing.T, db *sql.DB, size int64) domain.Hash {
	t.Helper()
	var hash domain.Hash
	copy(hash[:], newTestHash())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO chunks (hash, size_bytes) VALUES ($1, $2)`,
		hash[:], size,
	)
	require.NoError(t, err)
	return hash
}

func (h *apiHarness) attribute(t *testing.T, projectID snow.ID, size int64) {
	t.Helper()
	hash := seedAPIChunk(t, h.db, size)
	require.NoError(t, h.ledger.Attribute(context.Background(),
		&domain.Organization{ID: 1}, &domain.Project{ID: projectID},
		[]usecase.LedgerChunk{{Hash: hash, SizeBytes: size}}))
}

type orgStorageJSON struct {
	UsedBytes  int64  `json:"used_bytes"`
	QuotaBytes *int64 `json:"quota_bytes"`
	OwnerID    string `json:"owner_id"`
	Projects   []struct {
		ID        string `json:"id"`
		Slug      string `json:"slug"`
		UsedBytes int64  `json:"used_bytes"`
	} `json:"projects"`
}

type ownerStorageJSON struct {
	UserID     string `json:"user_id"`
	UsedBytes  int64  `json:"used_bytes"`
	QuotaBytes *int64 `json:"quota_bytes"`
	Orgs       []struct {
		ID        string `json:"id"`
		UsedBytes int64  `json:"used_bytes"`
	} `json:"orgs"`
}

func TestAPI_OrgStorage(t *testing.T) {
	h := newAPIHarness(t)
	h.attribute(t, 1, 128)

	status, body := h.do(http.MethodGet, "/api/v1/orgs/default/storage", "super", nil)
	var storage orgStorageJSON
	h.decode(status, body, &storage)
	require.Equal(t, int64(128), storage.UsedBytes)
	require.Equal(t, "1", storage.OwnerID)
	require.Len(t, storage.Projects, 1)
	require.Equal(t, int64(128), storage.Projects[0].UsedBytes)
	require.Nil(t, storage.QuotaBytes)

	status, _ = h.do(http.MethodGet, "/api/v1/orgs/missing/storage", "super", nil)
	require.Equal(t, http.StatusNotFound, status)

	status, _ = h.do(http.MethodGet, "/api/v1/orgs/default/storage", "", nil)
	require.Equal(t, http.StatusUnauthorized, status)

	status, _ = h.do(http.MethodGet, "/api/v1/orgs/default/storage", "user", nil)
	require.Equal(t, http.StatusForbidden, status)

	owner := seedAPIUser(t, h.db, "org-owner")
	seedAPIOrgMember(t, h.db, 1, owner, domain.OrgRoleOwner)
	h.validate.claims["org-owner"] = domain.Claims{UserID: owner}
	status, body = h.do(http.MethodGet, "/api/v1/orgs/default/storage", "org-owner", nil)
	h.decode(status, body, &storage)
	require.Equal(t, int64(128), storage.UsedBytes)
}

func TestAPI_OrgStorageDatabaseError(t *testing.T) {
	h := newAPIHarness(t)
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE org_usage`)
	require.NoError(t, err)

	status, body := h.do(http.MethodGet, "/api/v1/orgs/default/storage", "super", nil)
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_OrgStorageAuthorizationError(t *testing.T) {
	h := newAPIHarness(t)
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE org_members`)
	require.NoError(t, err)

	status, body := h.do(http.MethodGet, "/api/v1/orgs/default/storage", "user", nil)
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_OwnerStorage(t *testing.T) {
	h := newAPIHarness(t)
	h.attribute(t, 1, 256)

	status, body := h.do(http.MethodGet, "/api/v1/billing/me", "super", nil)
	var billing ownerStorageJSON
	h.decode(status, body, &billing)
	require.Equal(t, "1", billing.UserID)
	require.Equal(t, int64(256), billing.UsedBytes)
	require.Len(t, billing.Orgs, 1)
	require.Equal(t, int64(256), billing.Orgs[0].UsedBytes)

	status, body = h.do(http.MethodGet, "/api/v1/billing/me", "user", nil)
	h.decode(status, body, &billing)
	require.Equal(t, "2", billing.UserID)
	require.Zero(t, billing.UsedBytes)
	require.Empty(t, billing.Orgs)
}

func TestAPI_OwnerStorageDatabaseError(t *testing.T) {
	h := newAPIHarness(t)
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE owner_quota`)
	require.NoError(t, err)

	status, body := h.do(http.MethodGet, "/api/v1/billing/me", "super", nil)
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_SetOwnerQuota(t *testing.T) {
	h := newAPIHarness(t)

	quota := int64(1024)
	status, _ := h.do(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "user", map[string]any{"quota_bytes": quota})
	require.Equal(t, http.StatusForbidden, status)

	status, _ = h.do(http.MethodPut, "/api/v1/admin/billing/owners/bad_id/quota", "super", map[string]any{"quota_bytes": quota})
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.doRaw(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "super", "{not json")
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.do(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "super", map[string]any{"quota_bytes": int64(-1)})
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.do(http.MethodPut, "/api/v1/admin/billing/owners/999999/quota", "super", map[string]any{"quota_bytes": quota})
	require.Equal(t, http.StatusNotFound, status, "unknown owner must not surface as a database error")

	status, body := h.do(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "super", map[string]any{"quota_bytes": quota})
	var billing ownerStorageJSON
	h.decode(status, body, &billing)
	require.NotNil(t, billing.QuotaBytes)
	require.Equal(t, quota, *billing.QuotaBytes)

	status, body = h.do(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "super", map[string]any{"quota_bytes": nil})
	h.decode(status, body, &billing)
	require.Nil(t, billing.QuotaBytes)
}

func TestAPI_SetOwnerQuotaDatabaseError(t *testing.T) {
	h := newAPIHarness(t)
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE owner_quota`)
	require.NoError(t, err)

	status, body := h.do(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "super", map[string]any{"quota_bytes": int64(1)})
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_SetOwnerQuotaStorageReportError(t *testing.T) {
	h := newAPIHarness(t)
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE org_usage`)
	require.NoError(t, err)

	status, body := h.do(http.MethodPut, "/api/v1/admin/billing/owners/1/quota", "super", map[string]any{"quota_bytes": int64(1)})
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_SetBillingOwner(t *testing.T) {
	h := newAPIHarness(t)
	other := seedAPIUser(t, h.db, "billing-owner")

	status, _ := h.do(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "user", map[string]any{"user_id": other.Base36()})
	require.Equal(t, http.StatusForbidden, status)

	status, _ = h.do(http.MethodPut, "/api/v1/admin/orgs/missing/billing-owner", "super", map[string]any{"user_id": other.Base36()})
	require.Equal(t, http.StatusNotFound, status)

	status, _ = h.doRaw(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "super", "{not json")
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.do(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "super", map[string]any{"user_id": "!!!"})
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.do(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "super", map[string]any{"user_id": "999999"})
	require.Equal(t, http.StatusNotFound, status)

	status, body := h.do(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "super", map[string]any{"user_id": other.Base36()})
	var storage orgStorageJSON
	h.decode(status, body, &storage)
	require.Equal(t, other.Base36(), storage.OwnerID)
}

func TestAPI_SetBillingOwnerDatabaseError(t *testing.T) {
	h := newAPIHarness(t)
	other := seedAPIUser(t, h.db, "billing-owner")
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE org_billing_owner`)
	require.NoError(t, err)

	status, body := h.do(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "super", map[string]any{"user_id": other.Base36()})
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_SetBillingOwnerStorageReportError(t *testing.T) {
	h := newAPIHarness(t)
	other := seedAPIUser(t, h.db, "billing-owner")
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE project_usage`)
	require.NoError(t, err)

	status, body := h.do(http.MethodPut, "/api/v1/admin/orgs/default/billing-owner", "super", map[string]any{"user_id": other.Base36()})
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_RecountOrgStorage(t *testing.T) {
	h := newAPIHarness(t)
	h.attribute(t, 1, 512)

	status, _ := h.do(http.MethodPost, "/api/v1/admin/orgs/default/storage/recount", "user", nil)
	require.Equal(t, http.StatusForbidden, status)

	status, _ = h.do(http.MethodPost, "/api/v1/admin/orgs/missing/storage/recount", "super", nil)
	require.Equal(t, http.StatusNotFound, status)

	status, body := h.do(http.MethodPost, "/api/v1/admin/orgs/default/storage/recount", "super", nil)
	var storage orgStorageJSON
	h.decode(status, body, &storage)
	require.Zero(t, storage.UsedBytes, "no commit history, the stale link is dropped")
}

func TestAPI_RecountOrgStorageDatabaseError(t *testing.T) {
	h := newAPIHarness(t)
	_, err := h.db.ExecContext(context.Background(), `DROP TABLE commits CASCADE`)
	require.NoError(t, err)

	status, body := h.do(http.MethodPost, "/api/v1/admin/orgs/default/storage/recount", "super", nil)
	require.Equal(t, http.StatusInternalServerError, status, "body: %s", body)
}

func TestAPI_RegisterRoutesDisabled(t *testing.T) {
	// Without RegisterRoutes the routes are absent; with it they answer. The
	// 404 feature detection depends on the container having no storage routes.
	ws := new(restful.WebService).ApiVersion("1.0.0")
	ws.Path("/api/v1")
	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/orgs/default/storage")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAPI_MalformedOrgPath(t *testing.T) {
	h := newAPIHarness(t)
	status, _ := h.do(http.MethodGet, "/api/v1/orgs/%20/storage", "super", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestAPI_WriteError_Unknown(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(restful.NewResponse(rec), errors.New("boom"))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAPI_OwnerStorageMissingClaim(t *testing.T) {
	h := newAPIHarness(t)
	handlers := &handlers{cfg: RoutesConfig{Ledger: h.ledger}}
	rec := httptest.NewRecorder()
	handlers.ownerStorage(restful.NewRequest(&http.Request{}), restful.NewResponse(rec))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
