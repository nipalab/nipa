package suite_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
)

var _ = Describe("issue tracker", Ordered, func() {
	var (
		jira      *jiratest.Server
		trackerID string
	)

	BeforeAll(func() {
		jira = jiratest.New(jiratest.Options{})
		DeferCleanup(jira.Close)
		jira.AddIssue(jiratest.Issue{Key: "GAME-12", Summary: "New hero rig", Type: "Story", Status: "To Do"})

		var tracker struct {
			ID string `json:"id"`
		}
		status, body := api.doRaw(http.MethodPut, "/orgs/"+orgSlug+"/issue-tracker", map[string]any{
			"kind": "jira", "name": "Jira", "base_url": jira.URL, "auth_kind": "basic",
			"account": "bot@example.com", "token": "jira-test-token",
		})
		if status == http.StatusServiceUnavailable {
			Skip("the server runs without INTEGRATIONS_ENCRYPTION_KEY")
		}
		Expect(status).To(BeNumerically("<", 300), string(body))
		Expect(json.Unmarshal(body, &tracker)).To(Succeed())
		trackerID = tracker.ID
	})

	useTracker := func(project string) {
		GinkgoHelper()
		api.do(http.MethodPut, "/orgs/"+orgSlug+"/projects/"+project+"/issue-tracker", map[string]any{
			"tracker_id": trackerID, "keys": []string{"GAME"}, "link_commits": "off", "merged_status": "", "opened_status": "",
		}, nil, true)
	}

	It("names a branch after an issue and shows the keys in log and mr view", func() {
		project := newProject("issues")
		useTracker(project)
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-c", "--issue", "game-12")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("GAME-12: New hero rig [To Do]"))
		Expect(res.Output()).To(ContainSubstring(`Created and switched to branch "GAME-12-new-hero-rig"`))
		Expect(runNipa(dir, "branch").Output()).To(ContainSubstring("GAME-12-new-hero-rig"))

		writeText(dir, "rig.txt", "rig\n")
		Expect(runNipa(dir, "add", "rig.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "Fixes GAME-12 rig").ExitCode).To(Equal(0))

		log := runNipa(dir, "log", "--no-pager")
		Expect(log.ExitCode).To(Equal(0), log.Output())
		Expect(log.Output()).To(ContainSubstring("Issues: GAME-12"))
		oneline := runNipa(dir, "log", "--oneline", "--no-pager")
		Expect(oneline.ExitCode).To(Equal(0), oneline.Output())
		Expect(oneline.Output()).To(MatchRegexp(`Fixes GAME-12 rig.* \[GAME-12\]\n`))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits[0].IssueKeys).To(HaveLen(1))
		Expect(out.Commits[0].IssueKeys[0].Key).To(Equal("GAME-12"))
		Expect(out.Commits[0].IssueKeys[0].URL).To(Equal(jira.URL + "/browse/GAME-12"))

		created := runNipa(dir, "mr", "create", "--title", "Hero rig for GAME-12")
		Expect(created.ExitCode).To(Equal(0), created.Output())
		view := runNipa(dir, "mr", "view", "1")
		Expect(view.ExitCode).To(Equal(0), view.Output())
		Expect(view.Output()).To(ContainSubstring("issues:\n  GAME-12 " + jira.URL + "/browse/GAME-12"))
	})

	It("prefixes a given name, refuses unknown issues and skips the check without a tracker", func() {
		project := newProject("issues-name")
		useTracker(project)
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-c", "rig", "--issue", "GAME-12")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`"GAME-12-rig"`))

		missing := runNipa(dir, "branch", "--issue", "GAME-99")
		Expect(missing.ExitCode).NotTo(Equal(0))
		Expect(missing.Output()).To(ContainSubstring("issue GAME-99 was not found in the issue tracker"))

		other := runNipa(dir, "branch", "--issue", "ART-1")
		Expect(other.ExitCode).NotTo(Equal(0))
		Expect(other.Output()).To(ContainSubstring("ART-1 is not an issue of this project's issue tracker keys"))

		plain := newProject("issues-off")
		seedRepo(orgSlug, plain, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		offDir := cloneRepo(workspace(), repoURLFor(orgSlug, plain), "work")
		off := runNipa(offDir, "branch", "--issue", "GAME-5")
		Expect(off.ExitCode).To(Equal(0), off.Output())
		Expect(off.Output()).To(ContainSubstring("This project has no issue tracker; GAME-5 was not checked"))
		Expect(off.Output()).To(ContainSubstring(`"GAME-5"`))
	})
})
