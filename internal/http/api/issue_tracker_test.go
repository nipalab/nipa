package api

import (
	"database/sql"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func newTestIssueTracker(t *testing.T, db *sql.DB, orgs *usecase.Org, perm *usecase.Permission, node snow.Node, audit *usecase.Audit) (*usecase.IssueTracker, *jiratest.Server) {
	t.Helper()
	box, err := usecase.NewIssueTrackerBox([]byte(strings.Repeat("i", 32)))
	require.NoError(t, err)
	fake := jiratest.New(jiratest.Options{})
	t.Cleanup(fake.Close)
	uc := usecase.NewIssueTracker(sqlite.NewIssueTrackerRepository(db), orgs, perm, box, node).
		WithAudit(audit).
		WithFactory(usecase.GuardedIssueTrackerFactory([]string{"127.0.0.1"}))
	return uc, fake
}

func testIssueTrackerRoutes(t *testing.T, db *sql.DB, base, superTok string, fake *jiratest.Server, login func(t *testing.T, email string) string) {
	newUser := func(name string) (model.UserResponse, string) {
		resp := doMethod(t, http.MethodPost, base+"/api/v1/users",
			`{"name":"`+name+`","email":"`+name+`@example.com","password":"password123"}`, superTok)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		return decodeBody[model.UserResponse](t, resp), login(t, name+"@example.com")
	}
	expect := func(method, url, body, tok string, code int) *http.Response {
		t.Helper()
		resp := doMethod(t, method, url, body, tok)
		require.Equal(t, code, resp.StatusCode, method+" "+url)
		return resp
	}
	status := func(method, url, body, tok string, code int) {
		t.Helper()
		require.NoError(t, expect(method, url, body, tok, code).Body.Close())
	}
	_, ownerTok := newUser("it-owner")
	_, memberTok := newUser("it-member")
	padmin, padminTok := newUser("it-padmin")
	_, strangerTok := newUser("it-stranger")

	v1 := base + "/api/v1"
	orgURL := v1 + "/orgs/it-co"
	trackerURL := orgURL + "/issue-tracker"
	projectURL := orgURL + "/projects/game/issue-tracker"
	status(http.MethodPost, v1+"/orgs", `{"name":"IT Co","slug":"it-co"}`, ownerTok, http.StatusOK)
	status(http.MethodPost, orgURL+"/projects", `{"name":"Game","slug":"game"}`, ownerTok, http.StatusOK)
	status(http.MethodPost, orgURL+"/members", `{"email":"it-member@example.com","role":"member"}`, superTok, http.StatusOK)
	status(http.MethodPost, orgURL+"/members", `{"email":"it-padmin@example.com","role":"member"}`, superTok, http.StatusOK)
	status(http.MethodPost, orgURL+"/projects/game/permissions/rules",
		`{"user_id":"`+padmin.ID+`","path_prefix":"","permission":`+strconv.FormatUint(uint64(domain.PermissionAll), 10)+`}`, ownerTok, http.StatusOK)

	status(http.MethodGet, trackerURL, "", ownerTok, http.StatusNotFound)
	status(http.MethodGet, trackerURL, "", strangerTok, http.StatusNotFound)
	status(http.MethodPost, trackerURL+"/test", "{}", ownerTok, http.StatusNotFound)
	status(http.MethodPut, projectURL, `{"keys":["GAME"]}`, padminTok, http.StatusBadRequest)

	body := `{"base_url":"` + fake.URL + `","account":"bot@example.com","token":"jira-test-token"}`
	status(http.MethodPut, trackerURL, body, memberTok, http.StatusForbidden)
	status(http.MethodPut, trackerURL, `{"base_url":"ftp://x"}`, ownerTok, http.StatusBadRequest)
	status(http.MethodPut, trackerURL, `{`, ownerTok, http.StatusBadRequest)

	failed := expect(http.MethodPut, trackerURL, `{"base_url":"`+fake.URL+`","account":"bot@example.com","token":"wrong"}`, ownerTok, http.StatusBadRequest)
	probe := decodeBody[model.IssueTrackerProbeError](t, failed)
	require.Equal(t, "auth", probe.Check)
	require.NotContains(t, probe.Error, "wrong")

	created := decodeBody[model.IssueTrackerResponse](t, expect(http.MethodPut, trackerURL, body, ownerTok, http.StatusCreated))
	require.Equal(t, "jira", created.Kind)
	require.Equal(t, fake.URL, created.BaseURL)
	require.Equal(t, "basic", created.AuthKind)
	require.True(t, created.CanManage)
	require.Equal(t, "cloud", created.Site.Deployment)

	replaced := decodeBody[model.IssueTrackerResponse](t, expect(http.MethodPut, trackerURL,
		`{"name":"Studio Jira","base_url":"`+fake.URL+`","account":"bot@example.com"}`, ownerTok, http.StatusOK))
	require.Equal(t, created.ID, replaced.ID)
	require.Equal(t, "Studio Jira", replaced.Name)

	full := decodeBody[model.IssueTrackerResponse](t, expect(http.MethodGet, trackerURL, "", ownerTok, http.StatusOK))
	require.Equal(t, "bot@example.com", full.Account)
	limited := decodeBody[model.IssueTrackerResponse](t, expect(http.MethodGet, trackerURL, "", memberTok, http.StatusOK))
	require.Equal(t, model.IssueTrackerResponse{Kind: "jira", Name: "Studio Jira", BaseURL: fake.URL}, limited)

	test := decodeBody[model.IssueTrackerTestResponse](t, expect(http.MethodPost, trackerURL+"/test", "{}", ownerTok, http.StatusOK))
	require.True(t, test.OK)
	require.NotNil(t, test.Site)
	status(http.MethodPost, trackerURL+"/test", "{}", memberTok, http.StatusForbidden)

	status(http.MethodGet, projectURL, "", padminTok, http.StatusNotFound)
	status(http.MethodPut, projectURL, `{"keys":["GAME"]}`, memberTok, http.StatusForbidden)
	status(http.MethodPut, projectURL, `{"keys":["GAME"],"tracker_id":"!!"}`, padminTok, http.StatusBadRequest)
	status(http.MethodPut, projectURL, `{"keys":["GAME"],"tracker_id":"zz"}`, padminTok, http.StatusBadRequest)
	status(http.MethodPut, projectURL, `{"keys":[]}`, padminTok, http.StatusBadRequest)
	status(http.MethodPut, projectURL, `{`, padminTok, http.StatusBadRequest)
	settings := decodeBody[model.ProjectIssueTrackerResponse](t, expect(http.MethodPut, projectURL,
		`{"tracker_id":"`+created.ID+`","keys":["game","art"],"link_commits":"all","merged_status":"Done","comment_on_merge":false}`, padminTok, http.StatusOK))
	require.Equal(t, []string{"ART", "GAME"}, settings.Keys)
	require.Equal(t, created.ID, settings.TrackerID)
	require.True(t, settings.LinkMergeRequests)
	require.False(t, settings.CommentOnMerge)
	require.True(t, settings.Active)
	require.Equal(t, padmin.ID, settings.SetBy)
	read := decodeBody[model.ProjectIssueTrackerResponse](t, expect(http.MethodGet, projectURL, "", padminTok, http.StatusOK))
	require.Equal(t, settings.Keys, read.Keys)

	issuesURL := orgURL + "/projects/game/issues"
	fake.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig", Status: "Done"})
	resolved := decodeBody[model.IssueListResponse](t, expect(http.MethodGet, issuesURL+"?keys=game-1,GAME-2,OTHER-1", "", padminTok, http.StatusOK))
	require.Len(t, resolved.Issues, 2)
	require.Equal(t, model.IssueResponse{Key: "GAME-1", URL: fake.URL + "/browse/GAME-1", State: "ok", Title: "New rig", Type: "Task", Status: "Done", StatusCategory: "done"}, resolved.Issues[0])
	require.Equal(t, "not_found", resolved.Issues[1].State)
	status(http.MethodGet, issuesURL+"?keys=GAME-1", "", memberTok, http.StatusForbidden)
	status(http.MethodGet, issuesURL+"?keys="+strings.Repeat("GAME-1,", 51), "", padminTok, http.StatusBadRequest)
	found := decodeBody[model.IssueSearchResponse](t, expect(http.MethodGet, issuesURL+"/search?q=rig", "", padminTok, http.StatusOK))
	require.True(t, found.Supported)
	require.Len(t, found.Issues, 1)
	status(http.MethodGet, issuesURL+"/search", "", padminTok, http.StatusBadRequest)

	branches := decodeBody[[]model.BranchResponse](t, expect(http.MethodGet, orgURL+"/projects/game/branches", "", padminTok, http.StatusOK))
	require.NotEmpty(t, branches)
	require.Empty(t, branches[0].IssueKeys)
	branchID, err := snow.ParseBase36(branches[0].ID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO issue_links (id, project_id, tracker_id, issue_key, subject_kind, subject_id, relation, created_at)
		SELECT 1, project_id, ?, 'GAME-1', 'branch', id, 'mentions', CURRENT_TIMESTAMP FROM branches WHERE id = ?`, mustParseID(t, created.ID).Int64(), branchID.Int64())
	require.NoError(t, err)
	branches = decodeBody[[]model.BranchResponse](t, expect(http.MethodGet, orgURL+"/projects/game/branches", "", padminTok, http.StatusOK))
	require.Equal(t, []model.IssueKeyResponse{{Key: "GAME-1", URL: fake.URL + "/browse/GAME-1"}}, branches[0].IssueKeys)
	links := decodeBody[model.IssueLinksResponse](t, expect(http.MethodGet, issuesURL+"/GAME-1/links?limit=1", "", padminTok, http.StatusOK))
	require.Len(t, links.Links, 1)
	require.Equal(t, branches[0].Name, links.Links[0].Branch.Name)
	require.Empty(t, links.NextCursor)
	status(http.MethodGet, issuesURL+"/OTHER-1/links", "", padminTok, http.StatusBadRequest)
	status(http.MethodGet, issuesURL+"/GAME-1/links?after=!!", "", padminTok, http.StatusBadRequest)

	jobsURL := projectURL + "/jobs"
	trackerID := mustParseID(t, created.ID).Int64()
	for i, row := range []struct{ state, action, payload string }{
		{"failed", "link", `{"link_id":"nipa:it-co/game:mr:1","url":"https://nipa.example/it-co/game/merges/1","title":"#1 Rig"}`},
		{"delivered", "transition", `{"status":"Done"}`},
		{"pending", "comment", `{"text":"merged"}`},
	} {
		_, err = db.Exec(`INSERT INTO issue_tracker_jobs (id, tracker_id, project_id, issue_key, action, payload, dedupe_key, state, attempt, last_error, next_retry_at, created_at)
			SELECT ?, ?, id, 'GAME-1', ?, ?, ?, ?, 8, 'boom', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP FROM projects WHERE slug = 'game' AND org_id = (SELECT org_id FROM issue_trackers WHERE id = ?)`,
			100+i, trackerID, row.action, row.payload, "k"+strconv.Itoa(i), row.state, trackerID)
		require.NoError(t, err)
	}
	status(http.MethodGet, jobsURL, "", memberTok, http.StatusForbidden)
	status(http.MethodGet, jobsURL+"?state=bogus", "", padminTok, http.StatusBadRequest)
	status(http.MethodGet, jobsURL+"?after=!!", "", padminTok, http.StatusBadRequest)
	status(http.MethodGet, jobsURL+"?limit=x", "", padminTok, http.StatusBadRequest)
	page := decodeBody[model.IssueTrackerJobListResponse](t, expect(http.MethodGet, jobsURL+"?limit=2", "", padminTok, http.StatusOK))
	require.Len(t, page.Jobs, 2)
	require.NotEmpty(t, page.NextCursor)
	require.Equal(t, "comment", page.Jobs[0].Action)
	require.NotNil(t, page.Jobs[0].NextRetryAt)
	require.Equal(t, "Done", page.Jobs[1].Status)
	page = decodeBody[model.IssueTrackerJobListResponse](t, expect(http.MethodGet, jobsURL+"?after="+page.NextCursor, "", padminTok, http.StatusOK))
	require.Len(t, page.Jobs, 1)
	require.Equal(t, "#1 Rig", page.Jobs[0].Title)
	require.Equal(t, "https://nipa.example/it-co/game/merges/1", page.Jobs[0].URL)
	failedJobs := decodeBody[model.IssueTrackerJobListResponse](t, expect(http.MethodGet, jobsURL+"?state=failed", "", padminTok, http.StatusOK))
	require.Len(t, failedJobs.Jobs, 1)
	require.Equal(t, "boom", failedJobs.Jobs[0].LastError)
	retried := decodeBody[model.IssueTrackerJobResponse](t, expect(http.MethodPost, jobsURL+"/"+failedJobs.Jobs[0].ID+"/retry", "{}", padminTok, http.StatusOK))
	require.Equal(t, "pending", retried.State)
	require.Zero(t, retried.Attempts)
	require.Empty(t, retried.LastError)
	status(http.MethodPost, jobsURL+"/"+failedJobs.Jobs[0].ID+"/retry", "{}", padminTok, http.StatusConflict)
	status(http.MethodPost, jobsURL+"/"+failedJobs.Jobs[0].ID+"/retry", "{}", memberTok, http.StatusForbidden)
	status(http.MethodPost, jobsURL+"/zzzz/retry", "{}", padminTok, http.StatusNotFound)
	status(http.MethodPost, jobsURL+"/!!/retry", "{}", padminTok, http.StatusBadRequest)
	status(http.MethodGet, v1+"/orgs/it-co/projects/missing/issue-tracker/jobs", "", padminTok, http.StatusNotFound)
	status(http.MethodPost, v1+"/orgs/it-co/projects/missing/issue-tracker/jobs/1/retry", "{}", padminTok, http.StatusNotFound)

	status(http.MethodDelete, trackerURL, "", ownerTok, http.StatusConflict)
	status(http.MethodDelete, projectURL, "", memberTok, http.StatusForbidden)
	status(http.MethodDelete, projectURL, "", padminTok, http.StatusNoContent)
	status(http.MethodDelete, projectURL, "", padminTok, http.StatusNotFound)
	status(http.MethodGet, issuesURL+"?keys=GAME-1", "", padminTok, http.StatusNotFound)
	branches = decodeBody[[]model.BranchResponse](t, expect(http.MethodGet, orgURL+"/projects/game/branches", "", padminTok, http.StatusOK))
	require.Empty(t, branches[0].IssueKeys)
	status(http.MethodGet, v1+"/orgs/it-co/projects/missing/issue-tracker", "", padminTok, http.StatusNotFound)

	for _, action := range []string{"issue_tracker.created", "project.issue_tracker.set", "project.issue_tracker.cleared"} {
		audit := doGet(t, v1+"/admin/audit?action="+action, superTok)
		require.Equal(t, http.StatusOK, audit.StatusCode)
		raw, err := io.ReadAll(audit.Body)
		require.NoError(t, err)
		_ = audit.Body.Close()
		require.Contains(t, string(raw), `"action": "`+action+`"`)
		require.NotContains(t, string(raw), "jira-test-token")
	}

	status(http.MethodDelete, trackerURL, "", memberTok, http.StatusForbidden)
	status(http.MethodDelete, trackerURL, "", ownerTok, http.StatusNoContent)
	status(http.MethodDelete, trackerURL, "", ownerTok, http.StatusNotFound)
	status(http.MethodGet, v1+"/orgs/missing/issue-tracker", "", ownerTok, http.StatusNotFound)
	status(http.MethodPut, v1+"/orgs/missing/issue-tracker", body, ownerTok, http.StatusNotFound)
	status(http.MethodDelete, v1+"/orgs/missing/issue-tracker", "", ownerTok, http.StatusNotFound)
	status(http.MethodPost, v1+"/orgs/missing/issue-tracker/test", "{}", ownerTok, http.StatusNotFound)
	status(http.MethodPut, v1+"/orgs/it-co/projects/missing/issue-tracker", `{}`, ownerTok, http.StatusNotFound)
	status(http.MethodDelete, v1+"/orgs/it-co/projects/missing/issue-tracker", "", ownerTok, http.StatusNotFound)
}

func mustParseID(t *testing.T, raw string) snow.ID {
	t.Helper()
	id, err := snow.ParseBase36(raw)
	require.NoError(t, err)
	return id
}
