package suite_test

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type projectJSON struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type deletedProjectJSON struct {
	ID         string `json:"id"`
	OrgID      string `json:"org_id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	DeletedAt  string `json:"deleted_at"`
	PurgeAfter string `json:"purge_after"`
}

func (c *apiClient) getProject(org, project string) (int, projectJSON) {
	GinkgoHelper()
	status, data := c.doRaw(http.MethodGet, "/orgs/"+org+"/projects/"+project, nil)
	var out projectJSON
	if status == http.StatusOK {
		Expect(json.Unmarshal(data, &out)).To(Succeed())
	}
	return status, out
}

func (c *apiClient) deleteProject(org, project string) (int, []byte) {
	GinkgoHelper()
	return c.doRaw(http.MethodDelete, "/orgs/"+org+"/projects/"+project, nil)
}

func (c *apiClient) listDeletedProjects(org string) (int, []deletedProjectJSON) {
	GinkgoHelper()
	status, data := c.doRaw(http.MethodGet, "/orgs/"+org+"/deleted-projects", nil)
	var out []deletedProjectJSON
	if status == http.StatusOK {
		Expect(json.Unmarshal(data, &out)).To(Succeed(), "%s", data)
	}
	return status, out
}

func (c *apiClient) restoreProject(org, id string) (int, projectJSON, []byte) {
	GinkgoHelper()
	status, data := c.doRaw(http.MethodPost, "/orgs/"+org+"/deleted-projects/"+id+"/restore", nil)
	var out projectJSON
	if status == http.StatusOK {
		Expect(json.Unmarshal(data, &out)).To(Succeed())
	}
	return status, out, data
}

func findDeleted(list []deletedProjectJSON, slug string) *deletedProjectJSON {
	for i := range list {
		if list[i].Slug == slug {
			return &list[i]
		}
	}
	return nil
}

func listProjectSlugs(c *apiClient, org string) []string {
	GinkgoHelper()
	var projects []projectJSON
	c.do(http.MethodGet, "/orgs/"+org+"/projects", nil, &projects, true)
	slugs := make([]string, 0, len(projects))
	for _, p := range projects {
		slugs = append(slugs, p.Slug)
	}
	return slugs
}

// purgeRetentionDays is the restore window run.sh gives nipad-ee; zero (the
// free server) keeps deleted projects restorable.
func purgeRetentionDays() int {
	GinkgoHelper()
	raw := os.Getenv("NIPA_TEST_PURGE_RETENTION_DAYS")
	if raw == "" {
		return 0
	}
	days, err := strconv.Atoi(raw)
	Expect(err).NotTo(HaveOccurred())
	return days
}

func projectStorageBytes(storage orgStorageJSON, slug string) (int64, bool) {
	for _, p := range storage.Projects {
		if p.Slug == slug {
			return p.UsedBytes, true
		}
	}
	return 0, false
}

var _ = Describe("project delete and restore", func() {
	It("cuts the CLI off a deleted project and restores it with its history", func() {
		project := newProject("lifecycle")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("one\n")}, "seed lifecycle")
		repoURL := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, repoURL, "work")
		writeText(dir, "b.txt", "two\n")
		Expect(runNipa(dir, "add", "b.txt").ExitCode).To(Equal(0))
		second := uniqueMessage("second lifecycle commit")
		push := runNipa(dir, "push", "-m", second)
		Expect(push.ExitCode).To(Equal(0), push.Output())

		status, live := api.getProject(orgSlug, project)
		Expect(status).To(Equal(http.StatusOK))

		before := time.Now().Add(-time.Minute)
		status, body := api.deleteProject(orgSlug, project)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		Expect(string(body)).To(ContainSubstring("project deleted"))

		status, _ = api.getProject(orgSlug, project)
		Expect(status).To(Equal(http.StatusNotFound), "a deleted project is not resolvable by slug")
		Expect(listProjectSlugs(api, orgSlug)).NotTo(ContainElement(project))

		clone := runNipa(parent, "clone", repoURL, "gone")
		Expect(clone.ExitCode).To(Equal(127), clone.Output())
		Expect(clone.Output()).To(ContainSubstring("not found"))
		Expect(fileExists(parent, "gone/.nipa/config")).To(BeFalse())

		writeText(dir, "c.txt", "three\n")
		Expect(runNipa(dir, "add", "c.txt").ExitCode).To(Equal(0))
		pending := uniqueMessage("pushed after the restore")
		blocked := runNipa(dir, "push", "-m", pending)
		Expect(blocked.ExitCode).To(Equal(127), blocked.Output())
		update := runNipa(dir, "update")
		Expect(update.ExitCode).To(Equal(127), update.Output())
		logRes := runNipa(dir, "log")
		Expect(logRes.ExitCode).To(Equal(127), logRes.Output())

		status, deleted := api.listDeletedProjects(orgSlug)
		Expect(status).To(Equal(http.StatusOK))
		entry := findDeleted(deleted, project)
		Expect(entry).NotTo(BeNil(), "the deleted project is listed: %+v", deleted)
		Expect(entry.ID).To(Equal(live.ID))
		Expect(entry.OrgID).To(Equal(live.OrgID))
		Expect(entry.Name).To(Equal(live.Name))
		deletedAt, err := time.Parse(time.RFC3339, entry.DeletedAt)
		Expect(err).NotTo(HaveOccurred(), entry.DeletedAt)
		Expect(deletedAt).To(BeTemporally(">=", before))
		Expect(deletedAt).To(BeTemporally("<=", time.Now().Add(time.Minute)))
		if days := purgeRetentionDays(); days > 0 {
			purgeAfter, err := time.Parse(time.RFC3339, entry.PurgeAfter)
			Expect(err).NotTo(HaveOccurred(), "purge_after %q", entry.PurgeAfter)
			Expect(purgeAfter.Sub(deletedAt)).To(Equal(time.Duration(days)*24*time.Hour), "purge_after is deleted_at plus the retention")
		} else {
			Expect(entry.PurgeAfter).To(BeEmpty(), "without a purge retention deleted projects stay restorable")
		}

		status, restored, body := api.restoreProject(orgSlug, entry.ID)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		Expect(restored).To(Equal(live), "the restored project keeps its id, slug and name")

		status, deleted = api.listDeletedProjects(orgSlug)
		Expect(status).To(Equal(http.StatusOK))
		Expect(findDeleted(deleted, project)).To(BeNil())
		Expect(listProjectSlugs(api, orgSlug)).To(ContainElement(project))

		fresh := cloneRepo(parent, repoURL, "fresh")
		Expect(readText(fresh, "a.txt")).To(Equal("one\n"))
		Expect(readText(fresh, "b.txt")).To(Equal("two\n"))
		history := runNipa(fresh, "log")
		Expect(history.ExitCode).To(Equal(0), history.Output())
		Expect(history.Output()).To(ContainSubstring("seed lifecycle"))
		Expect(history.Output()).To(ContainSubstring(second))

		// The old working copy picks up where it left off.
		push = runNipa(dir, "push", "-m", pending)
		Expect(push.ExitCode).To(Equal(0), push.Output())
		Expect(runNipa(fresh, "update").ExitCode).To(Equal(0))
		Expect(readText(fresh, "c.txt")).To(Equal("three\n"))

		status, _, body = api.restoreProject(orgSlug, entry.ID)
		Expect(status).To(Equal(http.StatusNotFound), "a live project cannot be restored: %s", body)

		status, entries := listAudit(api, url.Values{"target": {live.ID}, "action": {"project."}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(auditActions(entries.Entries)).To(ContainElements("project.deleted", "project.restored"))
	})

	It("keeps a deleted project's slug reserved and refuses invalid restores", func() {
		project := newProject("lifecycle-slug")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed slug")
		status, live := api.getProject(orgSlug, project)
		Expect(status).To(Equal(http.StatusOK))

		status, body := api.deleteProject(orgSlug, project)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		status, body = api.deleteProject(orgSlug, project)
		Expect(status).To(Equal(http.StatusNotFound), "deleting twice: %s", body)

		status, body = api.doRaw(http.MethodPost, "/orgs/"+orgSlug+"/projects", map[string]string{"name": project, "slug": project})
		Expect(status).To(Equal(http.StatusConflict), "the slug stays taken until the purge: %s", body)

		other := newProject("lifecycle-live")
		_, otherProject := api.getProject(orgSlug, other)
		status, _, body = api.restoreProject(orgSlug, otherProject.ID)
		Expect(status).To(Equal(http.StatusNotFound), "a live project is not restorable: %s", body)
		status, _, body = api.restoreProject(orgSlug, "not-an-id!")
		Expect(status).To(Equal(http.StatusNotFound), "malformed id: %s", body)
		status, _, body = api.restoreProject(orgSlug, "zzzzzzzzzz")
		Expect(status).To(Equal(http.StatusNotFound), "unknown id: %s", body)

		elsewhere := "lifecycle-org-" + randSuffix(4)
		api.createOrg(elsewhere, elsewhere)
		status, _, body = api.restoreProject(elsewhere, live.ID)
		Expect(status).To(Equal(http.StatusNotFound), "restoring through another organization: %s", body)
		status, list := api.listDeletedProjects(elsewhere)
		Expect(status).To(Equal(http.StatusOK))
		Expect(list).To(BeEmpty())
		status, _ = api.listDeletedProjects("missing-org-" + randSuffix(4))
		Expect(status).To(Equal(http.StatusNotFound))

		status, restored, body := api.restoreProject(orgSlug, live.ID)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		Expect(restored.Slug).To(Equal(project), "the reserved slug comes back unchanged")
		cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
	})

	It("lets only organization owners and admins delete, list and restore", func() {
		owner := newIdentity("lifecycle-owner")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		org := "lifecycle-own-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		ownerAPI.createProject(org, "game", "game")
		repoURL := repoURLFor(org, "game")
		dir := cloneRepoAs(owner, workspace(), repoURL, "work")
		writeText(dir, "a.txt", "owned\n")
		Expect(runNipaAs(owner, dir, "add", "a.txt").ExitCode).To(Equal(0))
		push := runNipaAs(owner, dir, "push", "-m", uniqueMessage("owner content"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
		_, game := ownerAPI.getProject(org, "game")

		member := newIdentity("lifecycle-member")
		ownerAPI.inviteOrgMember(org, member.email, "member")
		memberAPI := newAPIClient(cli.apiURL, member.email, member.password)
		memberAPI.acceptInvitation(org)
		ownerAPI.createPathRule(org, "game", member.userID, "", 3)
		memberDir := cloneRepoAs(member, workspace(), repoURL, "member-work")

		outsider := newIdentity("lifecycle-outsider")
		outsiderAPI := newAPIClient(cli.apiURL, outsider.email, outsider.password)

		status, body := memberAPI.deleteProject(org, "game")
		Expect(status).To(Equal(http.StatusForbidden), "a member with write access cannot delete: %s", body)
		status, body = outsiderAPI.deleteProject(org, "game")
		Expect(status).To(Equal(http.StatusForbidden), "outsiders cannot delete: %s", body)
		status, _ = ownerAPI.getProject(org, "game")
		Expect(status).To(Equal(http.StatusOK), "refused deletes change nothing")

		status, body = ownerAPI.deleteProject(org, "game")
		Expect(status).To(Equal(http.StatusOK), "the owner deletes: %s", body)

		denied := runNipaAs(member, memberDir, "update")
		Expect(denied.ExitCode).To(Equal(127), denied.Output())

		status, _ = memberAPI.listDeletedProjects(org)
		Expect(status).To(Equal(http.StatusForbidden), "members cannot list deleted projects")
		status, _ = outsiderAPI.listDeletedProjects(org)
		Expect(status).To(Equal(http.StatusForbidden))
		status, _, body = memberAPI.restoreProject(org, game.ID)
		Expect(status).To(Equal(http.StatusForbidden), "members cannot restore: %s", body)
		status, _, body = outsiderAPI.restoreProject(org, game.ID)
		Expect(status).To(Equal(http.StatusForbidden), "outsiders cannot restore: %s", body)

		status, listed := ownerAPI.listDeletedProjects(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(findDeleted(listed, "game")).NotTo(BeNil())
		status, listed = api.listDeletedProjects(org)
		Expect(status).To(Equal(http.StatusOK), "global admins see every organization's deleted projects")
		Expect(findDeleted(listed, "game")).NotTo(BeNil())

		status, _, body = ownerAPI.restoreProject(org, game.ID)
		Expect(status).To(Equal(http.StatusOK), "the owner restores: %s", body)

		// The member's PBAC rule survives the round trip.
		update := runNipaAs(member, memberDir, "update")
		Expect(update.ExitCode).To(Equal(0), update.Output())
		Expect(readText(memberDir, "a.txt")).To(Equal("owned\n"))

		status, body = api.deleteProject(org, "game")
		Expect(status).To(Equal(http.StatusOK), "a global admin deletes: %s", body)
		status, _, body = api.restoreProject(org, game.ID)
		Expect(status).To(Equal(http.StatusOK), "a global admin restores: %s", body)
	})

	It("releases a deleted project's storage and charges it back on restore", func() {
		storageLedgerEnabled()
		if cli.edition == "free" {
			status, _ := api.getOrgStorage(orgSlug)
			Expect(status).To(Equal(http.StatusNotFound), "the free edition tracks no storage")
			return
		}

		owner := newIdentity("lifecycle-storage")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		org := "lifecycle-storage-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		ownerAPI.createProject(org, "keep", "keep")
		ownerAPI.createProject(org, "drop", "drop")
		pushContent := func(project, name string, size int) {
			GinkgoHelper()
			dir := cloneRepoAs(owner, workspace(), repoURLFor(org, project), project+"-"+randSuffix(2))
			writeText(dir, name, hex.EncodeToString(randomBytes(size)))
			Expect(runNipaAs(owner, dir, "add", name).ExitCode).To(Equal(0))
			push := runNipaAs(owner, dir, "push", "-m", uniqueMessage("content "+project))
			Expect(push.ExitCode).To(Equal(0), push.Output())
		}
		pushContent("keep", "keep.txt", 2048)
		pushContent("drop", "drop.txt", 4096)

		status, storage := ownerAPI.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		keepBytes, ok := projectStorageBytes(storage, "keep")
		Expect(ok).To(BeTrue())
		dropBytes, ok := projectStorageBytes(storage, "drop")
		Expect(ok).To(BeTrue())
		Expect(dropBytes).To(BeNumerically(">", 0))
		Expect(storage.UsedBytes).To(Equal(keepBytes + dropBytes))
		_, drop := ownerAPI.getProject(org, "drop")

		status, body := ownerAPI.deleteProject(org, "drop")
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		status, storage = ownerAPI.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(storage.UsedBytes).To(Equal(keepBytes), "the deleted project's bytes are released")
		if used, listed := projectStorageBytes(storage, "drop"); listed {
			Expect(used).To(BeZero())
		}
		Expect(ownerAPI.getMyBilling().UsedBytes).To(Equal(keepBytes))

		status, entries := listAudit(api, url.Values{"target": {drop.ID}, "action": {"project.deleted"}})
		Expect(status).To(Equal(http.StatusOK))
		Expect(entries.Entries).To(HaveLen(1))
		Expect(entries.Entries[0].Metadata).To(HaveKeyWithValue("released_bytes", BeNumerically("==", dropBytes)))

		// A restore that no longer fits the owner's quota is refused and
		// changes nothing.
		tight := keepBytes + 1
		status, _ = api.setOwnerQuota(owner.userID, &tight)
		Expect(status).To(Equal(http.StatusOK))
		DeferCleanup(func() {
			status, _ := api.setOwnerQuota(owner.userID, nil)
			Expect(status).To(Equal(http.StatusOK))
		})
		status, _, body = ownerAPI.restoreProject(org, drop.ID)
		Expect(status).To(Equal(http.StatusPaymentRequired), "%s", body)
		status, listed := ownerAPI.listDeletedProjects(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(findDeleted(listed, "drop")).NotTo(BeNil(), "a refused restore leaves the project deleted")
		status, storage = ownerAPI.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(storage.UsedBytes).To(Equal(keepBytes))

		roomy := int64(64 << 20)
		status, _ = api.setOwnerQuota(owner.userID, &roomy)
		Expect(status).To(Equal(http.StatusOK))
		status, _, body = ownerAPI.restoreProject(org, drop.ID)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		status, storage = ownerAPI.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(storage.UsedBytes).To(Equal(keepBytes+dropBytes), "the restore charges the history back")
		restoredBytes, _ := projectStorageBytes(storage, "drop")
		Expect(restoredBytes).To(Equal(dropBytes))

		status, recounted := api.recountOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(recounted.UsedBytes).To(Equal(keepBytes+dropBytes), "a recount agrees with the restored usage")
	})
})
