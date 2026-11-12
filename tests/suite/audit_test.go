package suite_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type auditEntryJSON struct {
	ID          string         `json:"id"`
	OccurredAt  time.Time      `json:"occurred_at"`
	ActorType   string         `json:"actor_type"`
	ActorUserID string         `json:"actor_user_id"`
	Action      string         `json:"action"`
	OrgID       string         `json:"org_id"`
	ProjectID   string         `json:"project_id"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	Metadata    map[string]any `json:"metadata"`
}

type auditListJSON struct {
	Entries    []auditEntryJSON `json:"entries"`
	NextCursor string           `json:"next_cursor"`
}

func listAudit(c *apiClient, query url.Values) (int, auditListJSON) {
	GinkgoHelper()
	status, data := c.doRaw(http.MethodGet, "/admin/audit?"+query.Encode(), nil)
	var out auditListJSON
	if status == http.StatusOK {
		Expect(json.Unmarshal(data, &out)).To(Succeed())
	}
	return status, out
}

func auditActions(entries []auditEntryJSON) []string {
	actions := make([]string, 0, len(entries))
	for _, entry := range entries {
		actions = append(actions, entry.Action)
	}
	return actions
}

func withoutLogins(entries []auditEntryJSON) []auditEntryJSON {
	out := make([]auditEntryJSON, 0, len(entries))
	for _, entry := range entries {
		if entry.Action != "auth.login.succeeded" {
			out = append(out, entry)
		}
	}
	return out
}

var _ = Describe("audit log REST API", func() {
	It("filters entries by actor, org, project, action and time and pages with next_cursor", func() {
		actor := newIdentity("audit-actor")
		actorAPI := newAPIClient(cli.apiURL, actor.email, actor.password)
		start := time.Now().UTC().Add(-2 * time.Second)

		org := "audit-org-" + randSuffix(4)
		var orgOut struct {
			ID string `json:"id"`
		}
		actorAPI.do(http.MethodPost, "/orgs", map[string]string{"name": org, "slug": org}, &orgOut, true)
		var projectOut struct {
			ID string `json:"id"`
		}
		actorAPI.do(http.MethodPost, "/orgs/"+org+"/projects", map[string]string{"name": "game", "slug": "game"}, &projectOut, true)
		seedRepo(org, "game", map[string][]byte{"a.txt": []byte("a\n")}, "audit seed")
		actorAPI.setBranchProtection(org, "game", "main", true, nil, nil)
		end := time.Now().UTC().Add(2 * time.Second)

		status, all := listAudit(api, url.Values{"actor": {actor.userID}})
		Expect(status).To(Equal(http.StatusOK))
		changes := withoutLogins(all.Entries)
		Expect(auditActions(changes)).To(Equal([]string{"branch.protection.changed", "project.created", "org.created"}))
		Expect(len(all.Entries)).To(BeNumerically(">", len(changes)), "the actor's sign-ins are recorded")
		Expect(all.NextCursor).To(BeEmpty())
		for _, entry := range all.Entries {
			Expect(entry.ActorType).To(Equal("user"))
			Expect(entry.ActorUserID).To(Equal(actor.userID))
			if entry.Action == "auth.login.succeeded" {
				Expect(entry.TargetID).To(Equal(actor.userID))
				Expect(entry.Metadata).To(HaveKeyWithValue("method", "password"))
			}
		}
		Expect(changes[0].ProjectID).To(Equal(projectOut.ID))
		Expect(changes[0].Metadata).To(HaveKeyWithValue("branch", "main"))
		Expect(changes[1].TargetID).To(Equal(projectOut.ID))
		Expect(changes[2].OrgID).To(Equal(orgOut.ID))

		status, byOrg := listAudit(api, url.Values{"org": {orgOut.ID}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(byOrg.Entries)).To(ContainElements("org.created", "project.created"))
		for _, entry := range byOrg.Entries {
			Expect(entry.OrgID).To(Equal(orgOut.ID))
		}

		status, byProject := listAudit(api, url.Values{"project": {projectOut.ID}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(byProject.Entries)).To(ConsistOf("branch.protection.changed", "project.created"))

		status, byAction := listAudit(api, url.Values{"actor": {actor.userID}, "action": {"project."}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(byAction.Entries)).To(Equal([]string{"project.created"}))

		status, byTarget := listAudit(api, url.Values{"target": {projectOut.ID}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(byTarget.Entries)).To(Equal([]string{"project.created"}))

		status, inWindow := listAudit(api, url.Values{
			"actor": {actor.userID},
			"since": {start.Format(time.RFC3339)},
			"until": {end.Format(time.RFC3339)},
		})
		Expect(status).To(Equal(http.StatusOK))
		Expect(withoutLogins(inWindow.Entries)).To(HaveLen(3))

		status, before := listAudit(api, url.Values{
			"actor": {actor.userID},
			"since": {start.Add(-time.Hour).Format(time.RFC3339)},
			"until": {start.Format(time.RFC3339)},
		})
		Expect(status).To(Equal(http.StatusOK))
		Expect(withoutLogins(before.Entries)).To(BeEmpty())

		status, after := listAudit(api, url.Values{"actor": {actor.userID}, "since": {end.Format(time.RFC3339)}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(withoutLogins(after.Entries)).To(BeEmpty())

		var paged []auditEntryJSON
		cursor := ""
		for range len(all.Entries) + 1 {
			query := url.Values{"actor": {actor.userID}, "limit": {"1"}}
			if cursor != "" {
				query.Set("after", cursor)
			}
			status, page := listAudit(api, query)
			Expect(status).To(Equal(http.StatusOK))
			Expect(len(page.Entries)).To(BeNumerically("<=", 1))
			paged = append(paged, page.Entries...)
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
			Expect(cursor).To(Equal(page.Entries[0].ID))
		}
		Expect(cursor).To(BeEmpty())
		Expect(paged).To(Equal(all.Entries))
	})

	It("records failed password logins without the password", func() {
		target := newIdentity("audit-login")
		status, _ := api.doAnonRaw(http.MethodPost, "/auth/login", map[string]string{
			"email": target.email, "password": "wrong-audit-password",
		})
		Expect(status).To(Equal(http.StatusBadRequest))

		status, failed := listAudit(api, url.Values{"action": {"auth.login.failed"}, "target": {target.userID}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(failed.Entries).To(HaveLen(1))
		entry := failed.Entries[0]
		Expect(entry.ActorType).To(Equal("system"))
		Expect(entry.ActorUserID).To(BeEmpty())
		Expect(entry.Metadata).To(Equal(map[string]any{
			"method": "password", "email": target.email, "reason": "invalid_credentials",
		}))
	})

	It("rejects non-super-admins and invalid filters", func() {
		member := newIdentity("audit-member")
		status, _ := newAPIClient(cli.apiURL, member.email, member.password).doRaw(http.MethodGet, "/admin/audit", nil)
		Expect(status).To(Equal(http.StatusForbidden))

		admin := newIdentity("audit-admin").promote()
		status, _ = newAPIClient(cli.apiURL, admin.email, admin.password).doRaw(http.MethodGet, "/admin/audit", nil)
		Expect(status).To(Equal(http.StatusForbidden))

		status, _ = api.doAnonRaw(http.MethodGet, "/admin/audit", nil)
		Expect(status).To(Equal(http.StatusUnauthorized))

		now := time.Now().UTC()
		for _, query := range []url.Values{
			{"actor": {"not base36!"}},
			{"since": {"yesterday"}},
			{"limit": {"-1"}},
			{"since": {now.Format(time.RFC3339)}, "until": {now.Format(time.RFC3339)}},
		} {
			status, data := api.doRaw(http.MethodGet, "/admin/audit?"+query.Encode(), nil)
			Expect(status).To(Equal(http.StatusBadRequest), "%v: %s", query, data)
		}
	})
})
