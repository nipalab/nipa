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

func testIssueTrackerRoutes(t *testing.T, base, superTok string, fake *jiratest.Server, login func(t *testing.T, email string) string) {
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
	_, ownerTok := newUser("it-owner")
	_, memberTok := newUser("it-member")
	padmin, padminTok := newUser("it-padmin")
	_, strangerTok := newUser("it-stranger")

	v1 := base + "/api/v1"
	orgURL := v1 + "/orgs/it-co"
	trackerURL := orgURL + "/issue-tracker"
	projectURL := orgURL + "/projects/game/issue-tracker"
	expect(http.MethodPost, v1+"/orgs", `{"name":"IT Co","slug":"it-co"}`, ownerTok, http.StatusOK).Body.Close()
	expect(http.MethodPost, orgURL+"/projects", `{"name":"Game","slug":"game"}`, ownerTok, http.StatusOK).Body.Close()
	expect(http.MethodPost, orgURL+"/members", `{"email":"it-member@example.com","role":"member"}`, superTok, http.StatusOK).Body.Close()
	expect(http.MethodPost, orgURL+"/members", `{"email":"it-padmin@example.com","role":"member"}`, superTok, http.StatusOK).Body.Close()
	expect(http.MethodPost, orgURL+"/projects/game/permissions/rules",
		`{"user_id":"`+padmin.ID+`","path_prefix":"","permission":`+strconv.FormatUint(uint64(domain.PermissionAll), 10)+`}`, ownerTok, http.StatusOK).Body.Close()

	expect(http.MethodGet, trackerURL, "", ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodGet, trackerURL, "", strangerTok, http.StatusForbidden).Body.Close()
	expect(http.MethodPost, trackerURL+"/test", "{}", ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodPut, projectURL, `{"keys":["GAME"]}`, padminTok, http.StatusBadRequest).Body.Close()

	body := `{"base_url":"` + fake.URL + `","account":"bot@example.com","token":"jira-test-token"}`
	expect(http.MethodPut, trackerURL, body, memberTok, http.StatusForbidden).Body.Close()
	expect(http.MethodPut, trackerURL, `{"base_url":"ftp://x"}`, ownerTok, http.StatusBadRequest).Body.Close()
	expect(http.MethodPut, trackerURL, `{`, ownerTok, http.StatusBadRequest).Body.Close()

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
	expect(http.MethodPost, trackerURL+"/test", "{}", memberTok, http.StatusForbidden).Body.Close()

	expect(http.MethodGet, projectURL, "", padminTok, http.StatusNotFound).Body.Close()
	expect(http.MethodPut, projectURL, `{"keys":["GAME"]}`, memberTok, http.StatusForbidden).Body.Close()
	expect(http.MethodPut, projectURL, `{"keys":["GAME"],"tracker_id":"!!"}`, padminTok, http.StatusBadRequest).Body.Close()
	expect(http.MethodPut, projectURL, `{"keys":["GAME"],"tracker_id":"zz"}`, padminTok, http.StatusBadRequest).Body.Close()
	expect(http.MethodPut, projectURL, `{"keys":[]}`, padminTok, http.StatusBadRequest).Body.Close()
	expect(http.MethodPut, projectURL, `{`, padminTok, http.StatusBadRequest).Body.Close()
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

	expect(http.MethodDelete, trackerURL, "", ownerTok, http.StatusConflict).Body.Close()
	expect(http.MethodDelete, projectURL, "", memberTok, http.StatusForbidden).Body.Close()
	expect(http.MethodDelete, projectURL, "", padminTok, http.StatusNoContent).Body.Close()
	expect(http.MethodDelete, projectURL, "", padminTok, http.StatusNotFound).Body.Close()
	expect(http.MethodGet, v1+"/orgs/it-co/projects/missing/issue-tracker", "", padminTok, http.StatusNotFound).Body.Close()

	for _, action := range []string{"issue_tracker.created", "project.issue_tracker.set", "project.issue_tracker.cleared"} {
		audit := doGet(t, v1+"/admin/audit?action="+action, superTok)
		require.Equal(t, http.StatusOK, audit.StatusCode)
		raw, err := io.ReadAll(audit.Body)
		require.NoError(t, err)
		_ = audit.Body.Close()
		require.Contains(t, string(raw), `"action": "`+action+`"`)
		require.NotContains(t, string(raw), "jira-test-token")
	}

	expect(http.MethodDelete, trackerURL, "", memberTok, http.StatusForbidden).Body.Close()
	expect(http.MethodDelete, trackerURL, "", ownerTok, http.StatusNoContent).Body.Close()
	expect(http.MethodDelete, trackerURL, "", ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodGet, v1+"/orgs/missing/issue-tracker", "", ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodPut, v1+"/orgs/missing/issue-tracker", body, ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodDelete, v1+"/orgs/missing/issue-tracker", "", ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodPost, v1+"/orgs/missing/issue-tracker/test", "{}", ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodPut, v1+"/orgs/it-co/projects/missing/issue-tracker", `{}`, ownerTok, http.StatusNotFound).Body.Close()
	expect(http.MethodDelete, v1+"/orgs/it-co/projects/missing/issue-tracker", "", ownerTok, http.StatusNotFound).Body.Close()
}
