package suite_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/securestorage"
)

func projectMaintenancePath(org, project string) string {
	return "/orgs/" + org + "/projects/" + project + "/maintenance"
}

func setProjectMaintenance(c *apiClient, org, project string, body map[string]any) {
	GinkgoHelper()
	c.do(http.MethodPut, projectMaintenancePath(org, project), body, nil, true)
	DeferCleanup(func() {
		status, _ := c.doRaw(http.MethodDelete, projectMaintenancePath(org, project), nil)
		Expect(status).To(Or(Equal(http.StatusNoContent), Equal(http.StatusNotFound)))
	})
}

func endProjectMaintenance(c *apiClient, org, project string) {
	GinkgoHelper()
	c.do(http.MethodDelete, projectMaintenancePath(org, project), nil, nil, true)
}

type publicMaintenance struct {
	Server *struct {
		Mode string `json:"mode"`
	} `json:"server"`
	Source string `json:"source"`
}

func serverMaintenanceMode() string {
	GinkgoHelper()
	resp, err := http.Get("http://" + cli.host + "/api/maintenance")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK), string(data))
	var body publicMaintenance
	Expect(json.Unmarshal(data, &body)).To(Succeed())
	if body.Server == nil {
		return ""
	}
	return body.Server.Mode
}

// setServerMaintenanceFile puts the whole server in maintenance through its
// MAINTENANCE_FILE override and waits for the poller to publish it.
func setServerMaintenanceFile(mode, message string) {
	GinkgoHelper()
	path := os.Getenv("NIPA_TEST_MAINTENANCE_FILE")
	if path == "" {
		Skip("NIPA_TEST_MAINTENANCE_FILE is not set; run the suite via tests/run.sh")
	}
	data, err := json.Marshal(map[string]string{"mode": mode, "message": message})
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, data, 0o644)).To(Succeed())
	DeferCleanup(clearServerMaintenanceFile)
	Eventually(serverMaintenanceMode, "15s", "200ms").Should(Equal(mode))
}

func clearServerMaintenanceFile() {
	GinkgoHelper()
	_ = os.Remove(os.Getenv("NIPA_TEST_MAINTENANCE_FILE"))
	Eventually(serverMaintenanceMode, "15s", "200ms").Should(BeEmpty())
}

func storedToken(tokenFile string) securestorage.AccessToken {
	GinkgoHelper()
	data, err := os.ReadFile(tokenFile)
	Expect(err).NotTo(HaveOccurred())
	tokens := map[string]securestorage.AccessToken{}
	Expect(json.Unmarshal(data, &tokens)).To(Succeed())
	token, ok := tokens[cli.host]
	Expect(ok).To(BeTrue(), "no token stored for %s", cli.host)
	return token
}

// breakAccessToken keeps the token's shape but spoils its signature, so the
// server answers Unauthenticated and the client has to refresh.
func breakAccessToken(tokenFile string) securestorage.AccessToken {
	GinkgoHelper()
	data, err := os.ReadFile(tokenFile)
	Expect(err).NotTo(HaveOccurred())
	tokens := map[string]securestorage.AccessToken{}
	Expect(json.Unmarshal(data, &tokens)).To(Succeed())
	token := tokens[cli.host]
	sig := token.AccessToken[len(token.AccessToken)-4:]
	token.AccessToken = token.AccessToken[:len(token.AccessToken)-4] + strings.Map(func(r rune) rune {
		if r == 'A' {
			return 'B'
		}
		return 'A'
	}, sig)
	tokens[cli.host] = token
	out, err := json.Marshal(tokens)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(tokenFile, out, 0o600)).To(Succeed())
	return token
}

var _ = Describe("maintenance mode", func() {
	It("keeps reads working and refuses writes with a clear message in a read-only project window", func() {
		project := newProject("maint-ro")
		other := newProject("maint-other")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n"), "art/hero.psd": {0x01, 0x02}}, "seed")
		seedRepo(orgSlug, other, map[string][]byte{"b.txt": []byte("b\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		otherDir := cloneRepo(parent, repoURLFor(orgSlug, other), "other")

		ends := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Minute)
		setProjectMaintenance(api, orgSlug, project, map[string]any{"mode": "read_only", "message": "storage move", "ends_at": ends.Format(time.RFC3339)})

		status := runNipa(dir, "status")
		Expect(status.ExitCode).To(Equal(0), status.Output())
		Expect(status.Output()).To(ContainSubstring(fmt.Sprintf("maintenance: project %s/%s read-only until ~%s: storage move", orgSlug, project, ends.Format("2006-01-02 15:04")+" UTC")))
		var st struct {
			Maintenance struct {
				Project struct {
					Mode   string `json:"mode"`
					Active bool   `json:"active"`
				} `json:"project"`
			} `json:"maintenance"`
		}
		Expect(runJSONInto(dir, &st, "status", "--json").ExitCode).To(Equal(0))
		Expect(st.Maintenance.Project.Mode).To(Equal("read_only"))
		Expect(st.Maintenance.Project.Active).To(BeTrue())

		Expect(runNipa(dir, "update").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "log").ExitCode).To(Equal(0))
		clone := runNipa(parent, "clone", repoURLFor(orgSlug, project), "again")
		Expect(clone.ExitCode).To(Equal(0), clone.Output())

		writeText(dir, "a.txt", "changed\n")
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))
		push := runNipa(dir, "push", "-m", uniqueMessage("refused"))
		Expect(push.ExitCode).To(Equal(1), push.Output())
		Expect(push.Stderr).To(ContainSubstring(fmt.Sprintf("project %s/%s is in maintenance (read-only): storage move", orgSlug, project)))
		Expect(push.Stderr).To(ContainSubstring("estimated end: " + ends.Format("2006-01-02 15:04") + " UTC"))
		Expect(push.Stderr).To(ContainSubstring("hint: read-only commands still work"))

		var pushErr struct {
			Error struct {
				Code        int    `json:"code"`
				Hint        string `json:"hint"`
				Maintenance struct {
					Scope string `json:"scope"`
					Mode  string `json:"mode"`
				} `json:"maintenance"`
			} `json:"error"`
		}
		jsonPush := runNipaEnv(dir, []string{"NIPA_OUTPUT=json"}, "push", "-m", uniqueMessage("refused json"))
		Expect(jsonPush.ExitCode).To(Equal(1))
		Expect(json.Unmarshal([]byte(jsonPush.Stderr), &pushErr)).To(Succeed(), jsonPush.Stderr)
		Expect(pushErr.Error.Code).To(Equal(503))
		Expect(pushErr.Error.Maintenance.Scope).To(Equal("project"))
		Expect(pushErr.Error.Maintenance.Mode).To(Equal("read_only"))

		lock := runNipa(dir, "lock", "art/hero.psd")
		Expect(lock.ExitCode).To(Equal(1), lock.Output())
		Expect(lock.Stderr).To(ContainSubstring("is in maintenance (read-only)"))
		Expect(lock.Stderr).To(ContainSubstring("hint: read-only commands still work"))

		mr := runNipa(dir, "mr", "create", "--title", "During maintenance", "--source", "feature", "--target", "main")
		Expect(mr.ExitCode).To(Equal(1), mr.Output())
		Expect(mr.Stderr).To(ContainSubstring("is in maintenance (read-only)"))
		Expect(runNipa(dir, "mr", "list").ExitCode).To(Equal(0))

		writeText(otherDir, "b.txt", "changed\n")
		Expect(runNipa(otherDir, "add", "b.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(otherDir, uniqueMessage("other project")).ExitCode).To(Equal(0), "another project is untouched")

		endProjectMaintenance(api, orgSlug, project)
		Expect(runNipa(dir, "status").Output()).NotTo(ContainSubstring("maintenance:"))
		Expect(pushRepo(dir, uniqueMessage("after maintenance")).ExitCode).To(Equal(0))
	})

	It("refuses reads in a closed project window and lets the org owner manage it", func() {
		owner := newIdentity("maint-owner")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		org := fmt.Sprintf("maint-org-%d-%s", time.Now().UnixNano(), randSuffix(3))
		ownerAPI.createOrg(org, org)
		project := "game"
		ownerAPI.createProject(org, project, project)

		member := newIdentity("maint-member")
		memberAPI := newAPIClient(cli.apiURL, member.email, member.password)
		status, _ := memberAPI.doRaw(http.MethodPut, projectMaintenancePath(org, project), map[string]any{"mode": "full"})
		Expect(status).To(Equal(http.StatusForbidden))

		setProjectMaintenance(ownerAPI, org, project, map[string]any{"mode": "full", "message": "repair"})
		clone := runNipaAs(owner, workspace(), "clone", repoURLFor(org, project), "work")
		Expect(clone.ExitCode).To(Equal(1), clone.Output())
		Expect(clone.Stderr).To(ContainSubstring(fmt.Sprintf("project %s/%s is in maintenance (closed): repair", org, project)))
		Expect(clone.Stderr).To(ContainSubstring("hint: try again later"))

		var version struct {
			Maintenance struct {
				Project struct {
					Mode string `json:"mode"`
				} `json:"project"`
			} `json:"maintenance"`
		}
		res := runNipaEnv(workspace(), []string{"NIPA_TOKEN_FILE=" + owner.tokenFile, "NIPA_OUTPUT=json"}, "version", "--server", repoURLFor(org, project))
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(json.Unmarshal([]byte(res.Stdout), &version)).To(Succeed(), res.Stdout)
		Expect(version.Maintenance.Project.Mode).To(Equal("full"))

		endProjectMaintenance(ownerAPI, org, project)
		Expect(runNipaAs(owner, workspace(), "clone", repoURLFor(org, project), "after").ExitCode).To(Equal(0))
	})

	It("keeps the stored session while the server is frozen", func() {
		project := newProject("maint-frozen")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		user := newIdentity("maint-frozen").promote()
		dir := cloneRepoAs(user, workspace(), repoURLFor(orgSlug, project), "work")

		setServerMaintenanceFile("frozen", "moving the database")

		Expect(runNipaAs(user, dir, "log").ExitCode).To(Equal(0), "reads work while the session lasts")
		version := runNipaAs(user, dir, "version", "--server")
		Expect(version.Output()).To(ContainSubstring("maintenance: server frozen: moving the database"))

		broken := breakAccessToken(user.tokenFile)
		res := runNipaAs(user, dir, "log")
		Expect(res.ExitCode).To(Equal(1), res.Output())
		Expect(res.Stderr).To(ContainSubstring("server is in maintenance (frozen): moving the database"))
		Expect(res.Stderr).To(ContainSubstring("hint: sign-in is paused"))
		Expect(storedToken(user.tokenFile)).To(Equal(broken), "the refused refresh keeps the stored session")

		writeText(dir, "a.txt", "frozen\n")
		Expect(runNipaAs(user, dir, "add", "a.txt").ExitCode).To(Equal(0))
		push := runNipaAs(user, dir, "push", "-m", uniqueMessage("frozen"))
		Expect(push.ExitCode).To(Equal(1), push.Output())
		Expect(push.Stderr).To(ContainSubstring("server is in maintenance (frozen)"))

		clearServerMaintenanceFile()
		res = runNipaAs(user, dir, "log")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(storedToken(user.tokenFile).RefreshToken).NotTo(Equal(broken.RefreshToken), "the kept session refreshes once the window ends")
		Expect(runNipaAs(user, dir, "push", "-m", uniqueMessage("after frozen")).ExitCode).To(Equal(0))
	})

	It("streams maintenance events from nipa serve and maps refusals in the JSON bridge", func() {
		project := newProject("maint-daemon")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		stream, err := d.client.WatchEvents(d.ctx, &daemonpb.WatchEventsRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		activity := make(chan *daemonpb.RepoActivity, 64)
		go func() {
			defer GinkgoRecover()
			for {
				a, err := stream.Recv()
				if err != nil {
					close(activity)
					return
				}
				activity <- a
			}
		}()
		var first *daemonpb.RepoActivity
		Eventually(activity, "10s").Should(Receive(&first))
		Expect(first.GetMaintenance()).To(BeNil())

		maintenanceEvent := func(phase string) func() *daemonpb.TeamEvent {
			return func() *daemonpb.TeamEvent {
				select {
				case a := <-activity:
					Expect(a.GetError()).To(BeEmpty())
					for _, e := range a.GetEvents() {
						if e.GetKind() == "maintenance" && e.GetMessage() == phase {
							return e
						}
					}
				default:
				}
				return nil
			}
		}

		setProjectMaintenance(api, orgSlug, project, map[string]any{"mode": "read_only", "message": "storage move"})
		started := maintenanceEvent("started")
		var event *daemonpb.TeamEvent
		Eventually(func() *daemonpb.TeamEvent { event = started(); return event }, "15s", "100ms").ShouldNot(BeNil())
		Expect(event.GetMaintenance().GetMode()).To(Equal("read_only"))
		Expect(event.GetMaintenance().GetProject()).To(Equal(orgSlug + "/" + project))

		resp, body := d.jsonPost("/v1/ProxyLockFile", fmt.Sprintf(`{"root":%q,"request":{"path":"a.txt"}}`, dir))
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable), string(body))
		var refused struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Maintenance struct {
				Scope string `json:"scope"`
				Mode  string `json:"mode"`
			} `json:"maintenance"`
		}
		Expect(json.Unmarshal(body, &refused)).To(Succeed())
		Expect(refused.Code).To(Equal("Unavailable"))
		Expect(refused.Maintenance.Scope).To(Equal("project"))
		Expect(refused.Maintenance.Mode).To(Equal("read_only"))

		endProjectMaintenance(api, orgSlug, project)
		ended := maintenanceEvent("ended")
		Eventually(ended, "15s", "100ms").ShouldNot(BeNil())
	})

	It("adds the maintenance hint to nipa mcp write tool errors", func() {
		project := newProject("maint-mcp")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		ends := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
		setProjectMaintenance(api, orgSlug, project, map[string]any{"mode": "read_only", "ends_at": ends.Format(time.RFC3339)})

		m := startMCP(dir, true)
		result := m.callTool("nipa_lock", map[string]any{"repo": dir, "path": "a.txt"})
		Expect(result.IsError).To(BeTrue(), result.text())
		Expect(result.text()).To(ContainSubstring("is in maintenance (read-only)"))
		Expect(result.text()).To(ContainSubstring("hint: read-only commands still work"))
		Expect(result.text()).To(ContainSubstring("next: retry after " + ends.Format(time.RFC3339)))
	})

	It("emails the org owner when a project window is announced and finished", func() {
		owner := newIdentity("maint-mail")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		org := fmt.Sprintf("maint-mail-%d-%s", time.Now().UnixNano(), randSuffix(3))
		ownerAPI.createOrg(org, org)
		ownerAPI.createProject(org, "game", "game")

		starts := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Minute)
		ownerAPI.do(http.MethodPut, projectMaintenancePath(org, "game"), map[string]any{"mode": "read_only", "message": "storage move", "starts_at": starts.Format(time.RFC3339), "notify": true}, nil, true)
		scheduled := email.waitForSubject(owner.email, "Maintenance scheduled: project "+org+"/game")
		Expect(scheduled.Text).To(ContainSubstring("storage move"))
		Expect(scheduled.InReplyTo).To(BeEmpty())

		endProjectMaintenance(ownerAPI, org, "game")
		finished := email.waitForSubject(owner.email, "Maintenance finished: project "+org+"/game")
		Expect(finished.InReplyTo).To(Equal(scheduled.MessageID), "the finished notice replies to the announcement")
	})
})
