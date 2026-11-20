package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
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

func issueLinkRows(t *testing.T, dbConn *sql.DB) []string {
	t.Helper()
	rows, err := dbConn.QueryContext(context.Background(), `SELECT subject_kind || ':' || issue_key || ':' || relation FROM issue_links ORDER BY subject_kind, issue_key`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var row string
		require.NoError(t, rows.Scan(&row))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

func testIssueTrackerLinks(t *testing.T, base, host, superToken string, dbConn *sql.DB) {
	ctx := context.Background()
	fake := jiratest.New(jiratest.Options{})
	t.Cleanup(fake.Close)
	fake.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "Hero rig", Status: "In Progress", Assignee: "Ana"})
	v1 := base + "/api/v1/orgs/" + e2eOrgSlug
	projectBase := v1 + "/projects/" + e2eProjectSlug
	issueTrackerREST(t, http.MethodPut, v1+"/issue-tracker",
		`{"base_url":"`+fake.URL+`","account":"bot@example.com","token":"jira-test-token"}`, superToken, http.StatusCreated, nil)
	issueTrackerREST(t, http.MethodPut, projectBase+"/issue-tracker", `{"keys":["GAME"]}`, superToken, http.StatusOK, nil)

	issueTrackerREST(t, http.MethodPut, projectBase+"/branches/main/protection", `{"protected":false}`, superToken, http.StatusOK, nil)
	grpcClient, auth, _ := newBenchClient(t, host)
	push := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	main := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, main, "rig.txt", "rig v1\n")
	stagePath(t, main, "rig.txt")
	require.NoError(t, push.Run(ctx, main, "Fixes GAME-1\n\nAlso touches OTHER-5 and UTF-8"))

	_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "GAME-2-rig", "main", "", "")
	require.NoError(t, err)
	feature := cloneWorktree(t, grpcClient, auth, host, "GAME-2-rig")
	writeFile(t, feature, "rig.txt", "rig v2\n")
	stagePath(t, feature, "rig.txt")
	require.NoError(t, push.Run(ctx, feature, "Tune the rig"))

	var mr model.MergeRequestResponse
	issueTrackerREST(t, http.MethodPost, projectBase+"/merge-requests",
		`{"title":"Rig for GAME-4","description":"Closes GAME-3","source_branch":"GAME-2-rig","target_branch":"main"}`, superToken, http.StatusOK, &mr)

	imported := importFiles(t, grpcClient, map[string]string{"legacy.txt": "old\n"})
	streamImport(t, grpcClient, []*clientgrpc.ImportCommit{{Origin: importGitOrigin(77), Message: "Fixes GAME-9", Files: imported}})
	_, err = grpcClient.ImportSetRefs(ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ImportRefs{
		Branches: []clientgrpc.ImportRef{{Name: "GAME-9-legacy", Origin: importGitOrigin(77)}}, CommitCount: 1,
	})
	require.NoError(t, err)

	require.Equal(t, []string{
		"branch:GAME-2:mentions",
		"commit:GAME-1:closes",
		"merge_request:GAME-2:mentions",
		"merge_request:GAME-3:closes",
		"merge_request:GAME-4:mentions",
	}, issueLinkRows(t, dbConn))

	var commits []model.CommitResponse
	issueTrackerREST(t, http.MethodGet, projectBase+"/commits?branch=main", "", superToken, http.StatusOK, &commits)
	require.Equal(t, []model.IssueKeyResponse{{Key: "GAME-1", URL: fake.URL + "/browse/GAME-1"}}, commits[0].IssueKeys)

	var detail model.MergeRequestResponse
	issueTrackerREST(t, http.MethodGet, projectBase+"/merge-requests/"+strconv.FormatInt(mr.Number, 10), "", superToken, http.StatusOK, &detail)
	require.Len(t, detail.IssueKeys, 3)
	var list model.MergeRequestListResponse
	issueTrackerREST(t, http.MethodGet, projectBase+"/merge-requests", "", superToken, http.StatusOK, &list)
	require.Equal(t, detail.IssueKeys, list.MergeRequests[0].IssueKeys)

	var branches []model.BranchResponse
	issueTrackerREST(t, http.MethodGet, projectBase+"/branches", "", superToken, http.StatusOK, &branches)
	keysByBranch := map[string]int{}
	for _, b := range branches {
		keysByBranch[b.Name] = len(b.IssueKeys)
	}
	require.Equal(t, 1, keysByBranch["GAME-2-rig"])
	require.Zero(t, keysByBranch["GAME-9-legacy"])

	var resolved model.IssueListResponse
	issueTrackerREST(t, http.MethodGet, projectBase+"/issues?keys=GAME-1,GAME-2,OTHER-5", "", superToken, http.StatusOK, &resolved)
	require.Len(t, resolved.Issues, 2)
	require.Equal(t, "Hero rig", resolved.Issues[0].Title)
	require.Equal(t, "in_progress", resolved.Issues[0].StatusCategory)
	require.Equal(t, "not_found", resolved.Issues[1].State)

	var links model.IssueLinksResponse
	issueTrackerREST(t, http.MethodGet, projectBase+"/issues/GAME-2/links", "", superToken, http.StatusOK, &links)
	require.Len(t, links.Links, 2)
	require.Equal(t, mr.Number, links.Links[0].MergeRequest.Number)
	require.Equal(t, "GAME-2-rig", links.Links[1].Branch.Name)
	links = model.IssueLinksResponse{}
	issueTrackerREST(t, http.MethodGet, projectBase+"/issues/GAME-1/links", "", superToken, http.StatusOK, &links)
	require.Equal(t, "Fixes GAME-1", links.Links[0].Commit.Title)

	issueTrackerREST(t, http.MethodPatch, projectBase+"/merge-requests/"+strconv.FormatInt(mr.Number, 10),
		`{"title":"Rig","description":"No issue here"}`, superToken, http.StatusOK, nil)
	require.Equal(t, []string{"branch:GAME-2:mentions", "commit:GAME-1:closes", "merge_request:GAME-2:mentions"}, issueLinkRows(t, dbConn))

	issueTrackerREST(t, http.MethodDelete, projectBase+"/issue-tracker", "", superToken, http.StatusNoContent, nil)
	commits = nil
	issueTrackerREST(t, http.MethodGet, projectBase+"/commits?branch=main", "", superToken, http.StatusOK, &commits)
	require.Empty(t, commits[0].IssueKeys)
	issueTrackerREST(t, http.MethodDelete, v1+"/issue-tracker", "", superToken, http.StatusNoContent, nil)
}
