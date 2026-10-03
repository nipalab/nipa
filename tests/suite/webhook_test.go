package suite_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type webhookFixture struct {
	receiver *hookReceiver
	hook     webhookJSON
}

func newWebhook(project string, events []string, extra map[string]any) webhookFixture {
	GinkgoHelper()

	receiver := startHookReceiver()
	body := map[string]any{
		"name":   "e2e-hook",
		"url":    receiver.url("/hook"),
		"events": events,
	}
	for key, value := range extra {
		body[key] = value
	}
	hook := api.createWebhook(orgSlug, project, body)
	Expect(hook.ID).NotTo(BeEmpty())
	Expect(hook.Secret).NotTo(BeEmpty(), "the signing secret is returned once on create")
	return webhookFixture{receiver: receiver, hook: hook}
}

func hookDeliveries(project, hookID string) []webhookDeliveryJSON {
	return api.listWebhookDeliveries(orgSlug, project, hookID)
}

var _ = Describe("webhook deliveries", func() {
	It("delivers a push event with file operations and a valid signature", func() {
		project := newProject("hook-push")
		seedRepo(orgSlug, project, map[string][]byte{
			"tracked.txt": []byte("v1\n"),
			"gone.txt":    []byte("gone\n"),
		}, "seed")
		fixture := newWebhook(project, []string{"push"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "tracked.txt", "v2\n")
		removePath(dir, "gone.txt")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "tracked.txt", "gone.txt", "new.txt").ExitCode).To(Equal(0))
		push := pushRepo(dir, "webhook push")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		delivery := fixture.receiver.wait("push")
		Expect(delivery.HookID).To(Equal(fixture.hook.ID))
		Expect(delivery.Delivery).NotTo(BeEmpty())
		Expect(delivery.Content).To(Equal("application/json"))
		Expect(delivery.Agent).To(Equal("Nipa/1.0"))
		Expect(delivery.verifySignature(fixture.hook.Secret)).To(BeTrue(), delivery.Signature)

		payload := delivery.pushPayload()
		Expect(payload.Event).To(Equal("push"))
		Expect(payload.Timestamp).NotTo(BeZero())
		Expect(payload.Actor.Username).To(Equal("Super Admin"))
		Expect(payload.Organization.Slug).To(Equal(orgSlug))
		Expect(payload.Project.Slug).To(Equal(project))
		Expect(payload.Project.DefaultBranch).To(Equal("main"))
		Expect(payload.WebhookID).To(Equal(fixture.hook.ID))

		Expect(payload.Changes).To(HaveLen(1))
		change := payload.Changes[0]
		Expect(change.Branch).To(Equal("main"))
		Expect(change.BranchID).NotTo(BeEmpty())
		Expect(change.Created).To(BeFalse())
		Expect(change.Deleted).To(BeFalse())
		Expect(change.Before).NotTo(BeEmpty())
		Expect(change.After).NotTo(BeEmpty())
		Expect(change.Message).To(ContainSubstring("webhook push"))

		files := map[string]hookPushFile{}
		for _, file := range change.Files {
			files[file.Path] = file
		}
		Expect(files).To(HaveLen(3))
		Expect(files["new.txt"].Operation).To(Equal("added"))
		Expect(files["tracked.txt"].Operation).To(Equal("modified"))
		Expect(files["tracked.txt"].SizeBytes).To(Equal(int64(3)))
		Expect(files["gone.txt"].Operation).To(Equal("deleted"))
	})

	It("delivers only the subscribed events", func() {
		project := newProject("hook-filter")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		pushFixture := newWebhook(project, []string{"push"}, nil)
		tagFixture := newWebhook(project, []string{"tag.created"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		writeText(dir, "b.txt", "b\n")
		Expect(runNipa(dir, "add", "b.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "push only").ExitCode).To(Equal(0))

		tagDelivery := tagFixture.receiver.wait("tag.created")
		Expect(tagDelivery.HookID).To(Equal(tagFixture.hook.ID))
		pushDelivery := pushFixture.receiver.wait("push")
		Expect(pushDelivery.HookID).To(Equal(pushFixture.hook.ID))

		Expect(hookDeliveries(project, tagFixture.hook.ID)).To(HaveLen(1))
		Expect(hookDeliveries(project, tagFixture.hook.ID)[0].EventType).To(Equal("tag.created"))
		Expect(hookDeliveries(project, pushFixture.hook.ID)).To(HaveLen(1))
		Expect(hookDeliveries(project, pushFixture.hook.ID)[0].EventType).To(Equal("push"))
	})

	It("filters push events by path prefix", func() {
		project := newProject("hook-path-prefix")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		fixture := newWebhook(project, []string{"push"}, map[string]any{"path_prefix": "src"})

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "docs/a.txt", "docs v2\n")
		Expect(runNipa(dir, "add", "docs/a.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "docs only").ExitCode).To(Equal(0))
		Expect(hookDeliveries(project, fixture.hook.ID)).To(BeEmpty(), "docs-only push must not match the src prefix")

		writeText(dir, "src/main.go", "package main // v2\n")
		Expect(runNipa(dir, "add", "src/main.go").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "src change").ExitCode).To(Equal(0))

		delivery := fixture.receiver.wait("push")
		payload := delivery.pushPayload()
		Expect(payload.Changes).To(HaveLen(1))
		paths := []string{}
		for _, file := range payload.Changes[0].Files {
			paths = append(paths, file.Path)
		}
		Expect(paths).To(Equal([]string{"src/main.go"}))
		Expect(fixture.receiver.count("push")).To(Equal(1))
	})

	It("delivers branch created and deleted events", func() {
		project := newProject("hook-branch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		fixture := newWebhook(project, []string{"branch.created", "branch.deleted"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))

		created := fixture.receiver.wait("branch.created").branchPayload()
		Expect(created.Event).To(Equal("branch.created"))
		Expect(created.Project.Slug).To(Equal(project))
		Expect(created.Branch.Name).To(Equal("feature"))
		Expect(created.Branch.ID).NotTo(BeEmpty())
		Expect(created.Branch.CommitID).NotTo(BeEmpty())
		Expect(created.Branch.Default).To(BeFalse())

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "branch", "-d", "feature").ExitCode).To(Equal(0))

		deleted := fixture.receiver.wait("branch.deleted").branchPayload()
		Expect(deleted.Branch.Name).To(Equal("feature"))
	})

	It("delivers tag created and deleted events", func() {
		project := newProject("hook-tag")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		fixture := newWebhook(project, []string{"tag.created", "tag.deleted"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0", "-m", "release one").ExitCode).To(Equal(0))

		created := fixture.receiver.wait("tag.created").tagPayload()
		Expect(created.Event).To(Equal("tag.created"))
		Expect(created.Tag.Name).To(Equal("v1.0.0"))
		Expect(created.Tag.Message).To(Equal("release one"))
		Expect(created.Tag.ID).NotTo(BeEmpty())
		Expect(created.Tag.CommitID).NotTo(BeEmpty())
		Expect(created.Tag.CreatedBy).NotTo(BeEmpty())

		Expect(runNipa(dir, "tag", "-d", "v1.0.0").ExitCode).To(Equal(0))
		deleted := fixture.receiver.wait("tag.deleted").tagPayload()
		Expect(deleted.Tag.Name).To(Equal("v1.0.0"))
	})

	It("delivers merge request created and updated events", func() {
		project := newProject("hook-mr-lifecycle")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		fixture := newWebhook(project, []string{"mr.created", "mr.updated"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "Hook MR", "-d", "first").ExitCode).To(Equal(0))

		created := fixture.receiver.wait("mr.created").mrPayload()
		Expect(created.Event).To(Equal("mr.created"))
		Expect(created.MergeRequest.Number).To(Equal(int64(1)))
		Expect(created.MergeRequest.Title).To(Equal("Hook MR"))
		Expect(created.MergeRequest.Description).To(Equal("first"))
		Expect(created.MergeRequest.State).To(Equal("open"))
		Expect(created.MergeRequest.SourceBranch).To(Equal("feature"))
		Expect(created.MergeRequest.TargetBranch).To(Equal("main"))
		Expect(created.MergeRequest.AuthorID).NotTo(BeEmpty())
		Expect(created.MergeRequest.CreatedAt).NotTo(BeZero())

		Expect(runNipa(dir, "mr", "update", "1", "--title", "Hook MR v2", "-d", "second").ExitCode).To(Equal(0))
		updated := fixture.receiver.wait("mr.updated").mrPayload()
		Expect(updated.MergeRequest.Number).To(Equal(int64(1)))
		Expect(updated.MergeRequest.Title).To(Equal("Hook MR v2"))
		Expect(updated.MergeRequest.Description).To(Equal("second"))
	})

	It("delivers a merge request merged event", func() {
		project := newProject("hook-mr-merged")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		fixture := newWebhook(project, []string{"mr.merged"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Hook merge")).ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "merge", "1").ExitCode).To(Equal(0))

		merged := fixture.receiver.wait("mr.merged").mrPayload()
		Expect(merged.Event).To(Equal("mr.merged"))
		Expect(merged.MergeRequest.Number).To(Equal(int64(1)))
		Expect(merged.MergeRequest.State).To(Equal("merged"))
		Expect(merged.MergeRequest.MergeCommitID).NotTo(BeEmpty())
	})

	It("delivers a merge request closed event", func() {
		project := newProject("hook-mr-closed")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		fixture := newWebhook(project, []string{"mr.closed"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "To close").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "close", "1").ExitCode).To(Equal(0))

		closed := fixture.receiver.wait("mr.closed").mrPayload()
		Expect(closed.Event).To(Equal("mr.closed"))
		Expect(closed.MergeRequest.Number).To(Equal(int64(1)))
		Expect(closed.MergeRequest.State).To(Equal("closed"))
	})

	It("delivers a merge request synchronized event on a source push", func() {
		project := newProject("hook-mr-sync")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		fixture := newWebhook(project, []string{"mr.synchronized"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "v1\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "Growing").ExitCode).To(Equal(0))

		writeText(dir, "feature.txt", "v2\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature follow-up").ExitCode).To(Equal(0))

		synced := fixture.receiver.wait("mr.synchronized").mrPayload()
		Expect(synced.Event).To(Equal("mr.synchronized"))
		Expect(synced.MergeRequest.Number).To(Equal(int64(1)))
		Expect(synced.MergeRequest.State).To(Equal("open"))
	})

	It("delivers a merge request reopened event", func() {
		project := newProject("hook-mr-reopened")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		fixture := newWebhook(project, []string{"mr.reopened"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "Reopen me").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "close", "1").ExitCode).To(Equal(0))

		api.reopenMergeRequest(orgSlug, project, 1)

		reopened := fixture.receiver.wait("mr.reopened").mrPayload()
		Expect(reopened.Event).To(Equal("mr.reopened"))
		Expect(reopened.MergeRequest.Number).To(Equal(int64(1)))
		Expect(reopened.MergeRequest.State).To(Equal("open"))
	})

	It("does not deliver while the webhook is inactive", func() {
		project := newProject("hook-inactive")
		fixture := newWebhook(project, []string{"push"}, map[string]any{"is_active": false})

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "a\n")
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "inactive push").ExitCode).To(Equal(0))
		Expect(hookDeliveries(project, fixture.hook.ID)).To(BeEmpty())

		activated := api.updateWebhook(orgSlug, project, fixture.hook.ID, map[string]any{"is_active": true})
		Expect(activated.IsActive).To(BeTrue())

		writeText(dir, "b.txt", "b\n")
		Expect(runNipa(dir, "add", "b.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "active push").ExitCode).To(Equal(0))

		delivery := fixture.receiver.wait("push")
		Expect(delivery.HookID).To(Equal(fixture.hook.ID))
	})

	It("sends a ping from the test endpoint even for an inactive webhook", func() {
		project := newProject("hook-ping")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		fixture := newWebhook(project, []string{"push"}, map[string]any{"is_active": false})

		queued := api.testWebhook(orgSlug, project, fixture.hook.ID)
		Expect(queued.EventType).To(Equal("ping"))
		Expect(queued.ID).NotTo(BeEmpty())

		delivery := fixture.receiver.wait("ping")
		Expect(delivery.Delivery).To(Equal(queued.ID))
		Expect(delivery.HookID).To(Equal(fixture.hook.ID))
		Expect(delivery.verifySignature(fixture.hook.Secret)).To(BeTrue(), delivery.Signature)

		payload := delivery.envelope()
		Expect(payload.Event).To(Equal("ping"))
		Expect(payload.Actor.Username).To(Equal("Super Admin"))
		Expect(payload.Organization.Slug).To(Equal(orgSlug))
		Expect(payload.Project.Slug).To(Equal(project))
		Expect(payload.WebhookID).To(Equal(fixture.hook.ID))
	})

	It("records deliveries in the REST ledger", func() {
		project := newProject("hook-ledger")
		fixture := newWebhook(project, []string{"push"}, nil)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "a\n")
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "ledger push").ExitCode).To(Equal(0))

		delivery := fixture.receiver.wait("push")
		Eventually(func() string {
			entries := hookDeliveries(project, fixture.hook.ID)
			if len(entries) != 1 {
				return fmt.Sprintf("entries=%d", len(entries))
			}
			return entries[0].State
		}, "10s", "100ms").Should(Equal("delivered"))

		entries := hookDeliveries(project, fixture.hook.ID)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].ID).To(Equal(delivery.Delivery))
		Expect(entries[0].WebhookID).To(Equal(fixture.hook.ID))
		Expect(entries[0].EventType).To(Equal("push"))
		Expect(entries[0].Attempt).To(Equal(int64(1)))
		Expect(entries[0].ResponseStatus).NotTo(BeNil())
		Expect(*entries[0].ResponseStatus).To(Equal(int64(200)))
		Expect(entries[0].DeliveredAt).NotTo(BeNil())
	})

	It("redelivers a failed delivery", func() {
		project := newProject("hook-redeliver")
		fixture := newWebhook(project, []string{"push"}, nil)
		fixture.receiver.setResponseCode(500)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "a\n")
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "failing push").ExitCode).To(Equal(0))

		first := fixture.receiver.wait("push")
		Eventually(func() string {
			entries := hookDeliveries(project, fixture.hook.ID)
			if len(entries) != 1 {
				return fmt.Sprintf("entries=%d", len(entries))
			}
			entry := entries[0]
			status := int64(0)
			if entry.ResponseStatus != nil {
				status = *entry.ResponseStatus
			}
			return fmt.Sprintf("%s/%d/%d", entry.State, entry.Attempt, status)
		}, "10s", "100ms").Should(Equal("pending/1/500"))
		Expect(hookDeliveries(project, fixture.hook.ID)[0].LastError).NotTo(BeEmpty())

		fixture.receiver.setResponseCode(200)
		redelivered := api.redeliverWebhook(orgSlug, project, fixture.hook.ID, first.Delivery)
		Expect(redelivered.ID).To(Equal(first.Delivery))

		second := fixture.receiver.waitCount("push", 2)
		Expect(second.Delivery).To(Equal(first.Delivery), "redelivery reuses the stored delivery id")

		Eventually(func() string {
			entries := hookDeliveries(project, fixture.hook.ID)
			if len(entries) != 1 {
				return fmt.Sprintf("entries=%d", len(entries))
			}
			return entries[0].State
		}, "10s", "100ms").Should(Equal("delivered"))
		entry := hookDeliveries(project, fixture.hook.ID)[0]
		Expect(entry.Attempt).To(BeNumerically(">=", int64(2)))
		Expect(entry.ResponseStatus).NotTo(BeNil())
		Expect(*entry.ResponseStatus).To(Equal(int64(200)))
	})

	It("signs deliveries with the rotated secret", func() {
		project := newProject("hook-rotate")
		fixture := newWebhook(project, []string{"push"}, nil)
		oldSecret := fixture.hook.Secret

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "a\n")
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "before rotation").ExitCode).To(Equal(0))

		before := fixture.receiver.wait("push")
		Expect(before.verifySignature(oldSecret)).To(BeTrue())

		rotated := api.rotateWebhookSecret(orgSlug, project, fixture.hook.ID)
		Expect(rotated.Secret).NotTo(BeEmpty())
		Expect(rotated.Secret).NotTo(Equal(oldSecret))

		writeText(dir, "b.txt", "b\n")
		Expect(runNipa(dir, "add", "b.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "after rotation").ExitCode).To(Equal(0))

		after := fixture.receiver.waitCount("push", 2)
		Expect(after.verifySignature(rotated.Secret)).To(BeTrue(), after.Signature)
		Expect(after.verifySignature(oldSecret)).To(BeFalse(), "the old secret must no longer validate")
	})
})
