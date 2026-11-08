package suite_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type serviceTokenJSON struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"`
	Scopes     []string   `json:"scopes"`
	CreatedBy  string     `json:"created_by"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	Token      string     `json:"token"`
}

func serviceTokensPath(org, project string) string {
	return fmt.Sprintf("/orgs/%s/projects/%s/service-tokens", org, project)
}

func (c *apiClient) createServiceToken(org, project, name string, scopes []string, expiresAt *time.Time) serviceTokenJSON {
	GinkgoHelper()
	body := map[string]any{"name": name, "scopes": scopes}
	if expiresAt != nil {
		body["expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	var out serviceTokenJSON
	c.do(http.MethodPost, serviceTokensPath(org, project), body, &out, true)
	Expect(out.Token).To(HavePrefix("nipa_st_"))
	return out
}

func (c *apiClient) listServiceTokens(org, project string) []serviceTokenJSON {
	GinkgoHelper()
	var out []serviceTokenJSON
	c.do(http.MethodGet, serviceTokensPath(org, project), nil, &out, true)
	return out
}

func findServiceToken(tokens []serviceTokenJSON, id string) serviceTokenJSON {
	GinkgoHelper()
	for _, token := range tokens {
		if token.ID == id {
			return token
		}
	}
	Fail("service token " + id + " not listed")
	return serviceTokenJSON{}
}

func projectPath(org, project string) string {
	return fmt.Sprintf("/orgs/%s/projects/%s", org, project)
}

func listCommitIDs(c *apiClient, org, project string) []string {
	GinkgoHelper()
	var commits []struct {
		ID string `json:"id"`
	}
	c.do(http.MethodGet, projectPath(org, project)+"/commits", nil, &commits, true)
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.ID)
	}
	return ids
}

type containsJSON struct {
	Commit    string `json:"commit"`
	Ref       string `json:"ref"`
	RefCommit string `json:"ref_commit"`
	Contains  bool   `json:"contains"`
}

func commitContains(c *apiClient, org, project, commit, ref string) containsJSON {
	GinkgoHelper()
	var out containsJSON
	c.do(http.MethodGet, projectPath(org, project)+"/commits/"+commit+"/contains?ref="+url.QueryEscape(ref), nil, &out, true)
	return out
}

func errorMessage(data []byte) string {
	var out struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(data, &out)
	return out.Message + out.Error
}

var _ = Describe("project service tokens", func() {
	It("creates, lists and revokes tokens with validation, admin gating and audit entries", func() {
		project := newProject("st-lifecycle")
		base := serviceTokensPath(orgSlug, project)

		created := api.createServiceToken(orgSlug, project, "  BugBase  ", []string{"webhooks:write", "read", "read"}, nil)
		Expect(created.ID).NotTo(BeEmpty())
		Expect(created.Name).To(Equal("BugBase"))
		Expect(created.Scopes).To(Equal([]string{"read", "webhooks:write"}))
		Expect(created.CreatedBy).To(Equal(api.me()))
		Expect(created.Hint).To(Equal(created.Token[:len("nipa_st_")+4]))
		Expect(created.RevokedAt).To(BeNil())

		listed := findServiceToken(api.listServiceTokens(orgSlug, project), created.ID)
		Expect(listed.Token).To(BeEmpty(), "the plaintext is only returned on create")
		Expect(listed.Hint).To(Equal(created.Hint))
		Expect(listed.LastUsedAt).To(BeNil())

		past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		for _, body := range []map[string]any{
			{"name": "", "scopes": []string{"read"}},
			{"name": strings.Repeat("x", 101), "scopes": []string{"read"}},
			{"name": "no scopes", "scopes": []string{}},
			{"name": "bad scope", "scopes": []string{"admin"}},
			{"name": "expired", "scopes": []string{"read"}, "expires_at": past},
		} {
			status, data := api.doRaw(http.MethodPost, base, body)
			Expect(status).To(Equal(http.StatusBadRequest), "%v: %s", body, data)
		}

		reader := newIdentity("st-reader")
		api.createPathRule(orgSlug, project, reader.userID, "", 3)
		readerAPI := newAPIClient(cli.apiURL, reader.email, reader.password)
		status, _ := readerAPI.doRaw(http.MethodGet, base, nil)
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = readerAPI.doRaw(http.MethodPost, base, map[string]any{"name": "x", "scopes": []string{"read"}})
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = readerAPI.doRaw(http.MethodDelete, base+"/"+created.ID, nil)
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = api.doAnonRaw(http.MethodGet, base, nil)
		Expect(status).To(Equal(http.StatusUnauthorized))

		tokenAPI := newAPIClientWithToken(cli.apiURL, created.Token)
		status, data := tokenAPI.doRaw(http.MethodGet, projectPath(orgSlug, project), nil)
		Expect(status).To(Equal(http.StatusOK), string(data))

		var revoked serviceTokenJSON
		api.do(http.MethodDelete, base+"/"+created.ID, nil, &revoked, true)
		Expect(revoked.RevokedAt).NotTo(BeNil())
		Expect(revoked.Token).To(BeEmpty())
		status, _ = api.doRaw(http.MethodDelete, base+"/"+created.ID, nil)
		Expect(status).To(Equal(http.StatusNotFound), "revoking twice is a 404")

		status, _ = tokenAPI.doRaw(http.MethodGet, projectPath(orgSlug, project), nil)
		Expect(status).To(Equal(http.StatusUnauthorized), "a revoked token is refused")

		listed = findServiceToken(api.listServiceTokens(orgSlug, project), created.ID)
		Expect(listed.RevokedAt).NotTo(BeNil(), "revoked tokens stay listed")

		status, audit := listAudit(api, url.Values{"target": {created.ID}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(audit.Entries)).To(Equal([]string{"service_token.revoked", "service_token.created"}))
		for _, entry := range audit.Entries {
			Expect(entry.ProjectID).To(Equal(created.ProjectID))
			Expect(entry.TargetType).To(Equal("service_token"))
			for _, value := range entry.Metadata {
				Expect(fmt.Sprint(value)).NotTo(ContainSubstring(created.Token), "the plaintext is never audited")
			}
		}
	})

	It("serves the read scope on its own project only and refuses every other route", func() {
		project := newProject("st-read")
		other := newProject("st-other")
		seedRepo(orgSlug, project, map[string][]byte{"fix.txt": []byte("fix\n")}, "fix crash")
		api.do(http.MethodPost, projectPath(orgSlug, project)+"/tags", map[string]string{
			"name": "v0.1", "from": "main",
		}, nil, true)
		seedRepo(orgSlug, project, map[string][]byte{"later.txt": []byte("later\n")}, "later work")

		token := api.createServiceToken(orgSlug, project, "reader", []string{"read"}, nil)
		tokenAPI := newAPIClientWithToken(cli.apiURL, token.Token)
		p := projectPath(orgSlug, project)

		ids := listCommitIDs(tokenAPI, orgSlug, project)
		Expect(ids).To(HaveLen(2))
		later, fix := ids[0], ids[1]
		Expect(ids).To(Equal(listCommitIDs(api, orgSlug, project)))

		for _, path := range []string{
			p,
			p + "/branches",
			p + "/commits/" + later + "/diff",
			p + "/tags",
			p + "/tags/v0.1",
			p + "/merge-requests",
		} {
			status, data := tokenAPI.doRaw(http.MethodGet, path, nil)
			Expect(status).To(Equal(http.StatusOK), "%s: %s", path, data)
		}

		inTag := commitContains(tokenAPI, orgSlug, project, fix, "v0.1")
		Expect(inTag.Contains).To(BeTrue())
		Expect(inTag.RefCommit).To(Equal(fix))
		Expect(commitContains(tokenAPI, orgSlug, project, later, "v0.1").Contains).To(BeFalse())
		Expect(commitContains(tokenAPI, orgSlug, project, fix, "main").Contains).To(BeTrue())
		Expect(commitContains(tokenAPI, orgSlug, project, fix, later).Contains).To(BeTrue())
		status, _ := tokenAPI.doRaw(http.MethodGet, p+"/commits/"+fix+"/contains?ref=no-such-ref", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		for _, path := range []string{
			p + "/tree",
			p + "/locks",
			p + "/service-tokens",
			p + "/permissions/rules",
			"/me",
			"/orgs",
		} {
			status, data := tokenAPI.doRaw(http.MethodGet, path, nil)
			Expect(status).To(Equal(http.StatusForbidden), "%s: %s", path, data)
		}

		status, data := tokenAPI.doRaw(http.MethodGet, projectPath(orgSlug, other), nil)
		Expect(status).To(Equal(http.StatusForbidden), string(data))
		Expect(errorMessage(data)).To(ContainSubstring("not valid for this project"))
		status, _ = tokenAPI.doRaw(http.MethodGet, projectPath(orgSlug, "no-such-project"), nil)
		Expect(status).To(Equal(http.StatusForbidden))

		status, data = tokenAPI.doRaw(http.MethodGet, p+"/webhooks", nil)
		Expect(status).To(Equal(http.StatusForbidden))
		Expect(errorMessage(data)).To(ContainSubstring("webhooks:write"))
		status, _ = tokenAPI.doRaw(http.MethodPost, p+"/merge-requests/1/checks", map[string]any{"name": "build", "state": "success"})
		Expect(status).To(Equal(http.StatusForbidden))

		forged := newAPIClientWithToken(cli.apiURL, "nipa_st_"+randSuffix(16))
		status, _ = forged.doRaw(http.MethodGet, p, nil)
		Expect(status).To(Equal(http.StatusUnauthorized))

		Eventually(func() *time.Time {
			return findServiceToken(api.listServiceTokens(orgSlug, project), token.ID).LastUsedAt
		}).WithTimeout(5 * time.Second).ShouldNot(BeNil())
	})

	It("reports merge request checks with checks:write, even on the creator's own request", func() {
		project := newProject("st-checks")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("CI gated")).ExitCode).To(Equal(0))

		required := true
		api.setBranchProtectionFull(orgSlug, project, "main", branchProtectionOptions{
			Protected:           true,
			RequireStatusChecks: &required,
			RequiredChecks:      []string{"build"},
		})
		check := api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("status_checks"))

		checksPath := fmt.Sprintf("%s/merge-requests/1/checks", projectPath(orgSlug, project))
		report := map[string]any{"name": "build", "state": "success", "details_url": "https://ci.example/1", "head_commit_id": check.SourceCommitID}
		status, _ := api.doRaw(http.MethodPost, checksPath, report)
		Expect(status).To(Equal(http.StatusForbidden), "the author's own session cannot vouch for the request")

		readOnly := newAPIClientWithToken(cli.apiURL, api.createServiceToken(orgSlug, project, "read only", []string{"read"}, nil).Token)
		status, _ = readOnly.doRaw(http.MethodPost, checksPath, report)
		Expect(status).To(Equal(http.StatusForbidden))

		ci := newAPIClientWithToken(cli.apiURL, api.createServiceToken(orgSlug, project, "ci", []string{"read", "checks:write"}, nil).Token)
		status, data := ci.doRaw(http.MethodPost, checksPath, report)
		Expect(status).To(Equal(http.StatusOK), string(data))
		Expect(api.checkMergeRequest(orgSlug, project, 1).BlockedBy).To(BeEmpty())

		var checks []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		}
		ci.do(http.MethodGet, checksPath, nil, &checks, true)
		Expect(checks).To(HaveLen(1))
		Expect(checks[0].Name).To(Equal("build"))
		Expect(checks[0].State).To(Equal("success"))

		for _, path := range []string{"/merge-requests/1", "/merge-requests/1/commits"} {
			status, data := ci.doRaw(http.MethodGet, projectPath(orgSlug, project)+path, nil)
			Expect(status).To(Equal(http.StatusOK), "%s: %s", path, data)
		}
		status, _ = ci.doRaw(http.MethodPost, projectPath(orgSlug, project)+"/merge-requests/1/merge", nil)
		Expect(status).To(Equal(http.StatusForbidden), "merging is not a service token route")

		merged := runNipa(dir, "mr", "merge", "1")
		Expect(merged.ExitCode).To(Equal(0), merged.Output())
	})

	It("manages webhooks with webhooks:write and tags audit entries with the token id", func() {
		project := newProject("st-webhooks")
		token := api.createServiceToken(orgSlug, project, "tracker", []string{"webhooks:write"}, nil)
		tokenAPI := newAPIClientWithToken(cli.apiURL, token.Token)

		receiver := startHookReceiver()
		hook := tokenAPI.createWebhook(orgSlug, project, map[string]any{
			"name":   "tracker-hook",
			"url":    receiver.url("/hook"),
			"events": []string{"push"},
		})
		Expect(hook.ID).NotTo(BeEmpty())
		Expect(hook.Secret).NotTo(BeEmpty())

		var hooks []webhookJSON
		tokenAPI.do(http.MethodGet, projectPath(orgSlug, project)+"/webhooks", nil, &hooks, true)
		Expect(hooks).To(HaveLen(1))

		updated := tokenAPI.updateWebhook(orgSlug, project, hook.ID, map[string]any{"events": []string{"push", "tag.created"}})
		Expect(updated.Events).To(ConsistOf("push", "tag.created"))

		tokenAPI.testWebhook(orgSlug, project, hook.ID)
		delivery := receiver.wait("ping")
		Expect(delivery.verifySignature(hook.Secret)).To(BeTrue())
		Eventually(func() []webhookDeliveryJSON {
			return tokenAPI.listWebhookDeliveries(orgSlug, project, hook.ID)
		}).WithTimeout(10 * time.Second).ShouldNot(BeEmpty())

		status, _ := tokenAPI.doRaw(http.MethodPost, projectPath(orgSlug, project)+"/webhooks/"+hook.ID+"/rotate-secret", nil)
		Expect(status).To(Equal(http.StatusForbidden), "rotating secrets is not a service token route")
		status, _ = tokenAPI.doRaw(http.MethodGet, projectPath(orgSlug, project), nil)
		Expect(status).To(Equal(http.StatusForbidden), "webhooks:write does not imply read")

		tokenAPI.do(http.MethodDelete, projectPath(orgSlug, project)+"/webhooks/"+hook.ID, nil, nil, true)

		status, audit := listAudit(api, url.Values{"target": {hook.ID}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(audit.Entries)).To(Equal([]string{"webhook.deleted", "webhook.updated", "webhook.created"}))
		for _, entry := range audit.Entries {
			Expect(entry.ActorType).To(Equal("user"))
			Expect(entry.ActorUserID).To(Equal(api.me()))
			Expect(entry.Metadata).To(HaveKeyWithValue("service_token_id", token.ID))
		}
	})

	It("stops working when it expires or its creator loses admin rights or is deleted", func() {
		project := newProject("st-creator")
		p := projectPath(orgSlug, project)

		expiresAt := time.Now().Add(3 * time.Second)
		short := newAPIClientWithToken(cli.apiURL, api.createServiceToken(orgSlug, project, "short", []string{"read"}, &expiresAt).Token)
		status, _ := short.doRaw(http.MethodGet, p, nil)
		Expect(status).To(Equal(http.StatusOK))
		Eventually(func() int {
			status, _ := short.doRaw(http.MethodGet, p, nil)
			return status
		}).WithTimeout(10 * time.Second).WithPolling(500 * time.Millisecond).Should(Equal(http.StatusUnauthorized))

		admin := newIdentity("st-admin")
		var rule struct {
			ID int64 `json:"id"`
		}
		api.do(http.MethodPost, p+"/permissions/rules", map[string]any{
			"user_id":     admin.userID,
			"path_prefix": "",
			"permission":  65536 | 7,
		}, &rule, true)
		adminAPI := newAPIClient(cli.apiURL, admin.email, admin.password)
		token := adminAPI.createServiceToken(orgSlug, project, "admin-made", []string{"read"}, nil)
		Expect(token.CreatedBy).To(Equal(admin.userID))
		tokenAPI := newAPIClientWithToken(cli.apiURL, token.Token)

		status, data := tokenAPI.doRaw(http.MethodGet, p, nil)
		Expect(status).To(Equal(http.StatusOK), string(data))

		api.do(http.MethodDelete, fmt.Sprintf("%s/permissions/rules/%d", p, rule.ID), nil, nil, true)
		status, data = tokenAPI.doRaw(http.MethodGet, p, nil)
		Expect(status).To(Equal(http.StatusForbidden), string(data))
		Expect(errorMessage(data)).To(ContainSubstring("no longer a project admin"))

		api.do(http.MethodDelete, "/users/"+admin.userID, nil, nil, true)
		status, _ = tokenAPI.doRaw(http.MethodGet, p, nil)
		Expect(status).To(Equal(http.StatusUnauthorized))
	})
})
