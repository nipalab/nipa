package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
)

var e2eIntegrationsKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))

func issueTrackerREST(t *testing.T, method, url, body, token string, code int, out any) {
	t.Helper()
	resp := auditREST(t, method, url, body, token)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, code, resp.StatusCode, method+" "+url)
	if out != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
	}
}

func testIssueTrackerConnection(t *testing.T, base, superToken string, dbConn *sql.DB) {
	fake := jiratest.New(jiratest.Options{AuthKind: "bearer", Secret: "dc-personal-token", Deployment: "Server"})
	t.Cleanup(fake.Close)
	orgURL := base + "/api/v1/orgs/" + e2eOrgSlug + "/issue-tracker"
	projectURL := base + "/api/v1/orgs/" + e2eOrgSlug + "/projects/" + e2eProjectSlug + "/issue-tracker"

	issueTrackerREST(t, http.MethodPut, projectURL, `{"keys":["GAME"]}`, superToken, http.StatusBadRequest, nil)

	var created model.IssueTrackerResponse
	issueTrackerREST(t, http.MethodPut, orgURL,
		`{"kind":"jira","name":"Studio Jira","base_url":"`+fake.URL+`/","auth_kind":"bearer","token":"dc-personal-token"}`,
		superToken, http.StatusCreated, &created)
	require.Equal(t, fake.URL, created.BaseURL)
	require.Equal(t, "bearer", created.AuthKind)
	require.Equal(t, "server", created.Site.Deployment)
	require.Equal(t, "nipa-bot", created.Site.AccountID)
	require.Equal(t, "Bearer dc-personal-token", fake.LastHeaders().Get("Authorization"))

	var sealed []byte
	require.NoError(t, dbConn.QueryRowContext(context.Background(), `SELECT secret_sealed FROM issue_trackers`).Scan(&sealed))
	require.NotEmpty(t, sealed)
	require.False(t, bytes.Contains(sealed, []byte("dc-personal-token")))

	var settings model.ProjectIssueTrackerResponse
	issueTrackerREST(t, http.MethodPut, projectURL,
		`{"keys":["game","art"],"link_commits":"default_branch","merged_status":"Done","opened_status":"In Review"}`,
		superToken, http.StatusOK, &settings)
	require.Equal(t, []string{"ART", "GAME"}, settings.Keys)
	require.Equal(t, created.ID, settings.TrackerID)

	var test model.IssueTrackerTestResponse
	issueTrackerREST(t, http.MethodPost, orgURL+"/test", `{}`, superToken, http.StatusOK, &test)
	require.True(t, test.OK)

	issueTrackerREST(t, http.MethodDelete, orgURL, "", superToken, http.StatusConflict, nil)
	issueTrackerREST(t, http.MethodDelete, projectURL, "", superToken, http.StatusNoContent, nil)

	var moved model.IssueTrackerProbeError
	issueTrackerREST(t, http.MethodPut, orgURL,
		`{"base_url":"https://jira.invalid","auth_kind":"bearer","token":"dc-personal-token"}`, superToken, http.StatusBadRequest, &moved)
	require.Equal(t, "reach", moved.Check)

	issueTrackerREST(t, http.MethodDelete, orgURL, "", superToken, http.StatusNoContent, nil)
	issueTrackerREST(t, http.MethodGet, orgURL, "", superToken, http.StatusNotFound, nil)

	var entries struct {
		Entries []model.AuditEntryResponse `json:"entries"`
	}
	issueTrackerREST(t, http.MethodGet, base+"/api/v1/admin/audit?action=issue_tracker", "", superToken, http.StatusOK, &entries)
	require.Len(t, entries.Entries, 2)
	for _, entry := range entries.Entries {
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "dc-personal-token")
	}
}
