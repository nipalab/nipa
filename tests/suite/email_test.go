package suite_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("email notifications", func() {
	It("emails merge request participants and honors opt-out", func() {
		project := newProject("email")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")

		author := newIdentity("email-author").promote()
		reviewer := newIdentity("email-reviewer").promote()
		reviewerAPI := newAPIClient(cli.apiURL, reviewer.email, reviewer.password)
		authorAPI := newAPIClient(cli.apiURL, author.email, author.password)

		parent := workspace()
		dir := cloneRepoAs(author, parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipaAs(author, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipaAs(author, dir, "add", "feature.txt").ExitCode).To(Equal(0))
		push := runNipaAs(author, dir, "push", "-m", uniqueMessage("email work"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
		create := runNipaAs(author, dir, "mr", "create", "--title", uniqueMessage("Email MR"))
		Expect(create.ExitCode).To(Equal(0), create.Output())

		request := runNipaAs(author, dir, "mr", "request-review", "1", reviewer.userID)
		Expect(request.ExitCode).To(Equal(0), request.Output())
		reviewEmail := email.waitForSubject(reviewer.email, "Review requested")
		Expect(reviewEmail.Subject).To(ContainSubstring("!1:"))
		Expect(reviewEmail.Text).To(ContainSubstring("requested a review on merge request !1"))
		Expect(reviewEmail.Text).To(ContainSubstring("/" + orgSlug + "/" + project + "/merges/1"))
		Expect(reviewEmail.HTML).To(ContainSubstring("href="))
		Expect(reviewEmail.MessageID).To(MatchRegexp(`-mr1-[0-9a-z]+@`), "the first contact carries the recipient's thread root")
		Expect(reviewEmail.InReplyTo).To(BeEmpty())
		Expect(email.countFor(author.email)).To(Equal(0), "the requesting author is not emailed")

		reviewerAPI.addMergeRequestComment(orgSlug, project, 1, "", "looks good")
		commentEmail := email.waitForSubject(author.email, "New comment")
		Expect(commentEmail.Text).To(ContainSubstring("commented on merge request !1"))
		Expect(commentEmail.MessageID).To(MatchRegexp(`-mr1-[0-9a-z]+@`))
		Expect(commentEmail.InReplyTo).To(BeEmpty())
		Expect(email.countFor(reviewer.email)).To(Equal(1), "the commenter gets no copy of their own comment")

		authorAPI.addMergeRequestComment(orgSlug, project, 1, "", "thanks")
		replyEmail := email.waitForSubject(reviewer.email, "New comment")
		Expect(replyEmail.InReplyTo).To(Equal(reviewEmail.MessageID), "replies reference the thread root")
		Expect(replyEmail.MessageID).NotTo(Equal(replyEmail.InReplyTo))

		Expect(authorAPI.do(http.MethodPatch, "/me", map[string]any{
			"name":         author.name,
			"notify_email": false,
		}, nil, true)).To(Equal(http.StatusOK))
		reviewerAPI.addMergeRequestComment(orgSlug, project, 1, "", "one more")
		Consistently(func() int { return email.countFor(author.email) }, "2s", "100ms").Should(Equal(1))
	})

	It("emails lifecycle events to participants except the actor, threaded per recipient", func() {
		project := newProject("email-lifecycle")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")

		author := newIdentity("email-life-author").promote()
		reviewer := newIdentity("email-life-reviewer").promote()
		assignee := newIdentity("email-life-assignee").promote()
		root := func(id *identity) string { return "-mr1-" + id.userID + "@" }
		expectRoot := func(msg receivedEmail, id *identity) {
			GinkgoHelper()
			Expect(msg.MessageID).To(ContainSubstring(root(id)), "first contact opens the recipient's thread")
			Expect(msg.InReplyTo).To(BeEmpty())
			Expect(msg.References).To(BeEmpty())
		}
		expectReply := func(msg, first receivedEmail) {
			GinkgoHelper()
			Expect(msg.InReplyTo).To(Equal(first.MessageID))
			Expect(msg.References).To(Equal(first.MessageID))
			Expect(msg.MessageID).NotTo(Equal(first.MessageID))
		}

		parent := workspace()
		dir := cloneRepoAs(author, parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipaAs(author, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipaAs(author, dir, "add", "feature.txt").ExitCode).To(Equal(0))
		push := runNipaAs(author, dir, "push", "-m", uniqueMessage("email lifecycle work"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
		create := runNipaAs(author, dir, "mr", "create", "--draft", "--title", uniqueMessage("Email lifecycle MR"),
			"--reviewer", reviewer.userID, "--assignee", assignee.userID)
		Expect(create.ExitCode).To(Equal(0), create.Output())

		reviewerFirst := email.waitForSubject(reviewer.email, "Merge request opened")
		Expect(reviewerFirst.Subject).To(ContainSubstring("[" + project + "]"))
		Expect(reviewerFirst.Text).To(ContainSubstring("opened merge request !1"))
		expectRoot(reviewerFirst, reviewer)
		assigneeFirst := email.waitForSubject(assignee.email, "Merge request opened")
		expectRoot(assigneeFirst, assignee)

		ready := runNipaAs(author, dir, "mr", "ready", "1")
		Expect(ready.ExitCode).To(Equal(0), ready.Output())
		assigneeReady := email.waitForSubject(assignee.email, "Merge request ready for review")
		Expect(assigneeReady.Text).To(ContainSubstring("marked ready for review merge request !1"))
		expectReply(assigneeReady, assigneeFirst)
		expectReply(email.waitForSubject(reviewer.email, "Merge request ready for review"), reviewerFirst)

		review := runNipaAs(reviewer, dir, "mr", "review", "1", "--approve", "-m", "lgtm")
		Expect(review.ExitCode).To(Equal(0), review.Output())
		authorFirst := email.waitForSubject(author.email, "Review submitted")
		Expect(authorFirst.Text).To(ContainSubstring("reviewed merge request !1"))
		expectRoot(authorFirst, author)
		expectReply(email.waitForSubject(assignee.email, "Review submitted"), assigneeFirst)

		closeMR := runNipaAs(reviewer, dir, "mr", "close", "1")
		Expect(closeMR.ExitCode).To(Equal(0), closeMR.Output())
		closed := email.waitForSubject(author.email, "Merge request closed")
		Expect(closed.Text).To(ContainSubstring("closed merge request !1"))
		expectReply(closed, authorFirst)
		expectReply(email.waitForSubject(assignee.email, "Merge request closed"), assigneeFirst)

		reopen := runNipaAs(author, dir, "mr", "reopen", "1")
		Expect(reopen.ExitCode).To(Equal(0), reopen.Output())
		reopened := email.waitForSubject(assignee.email, "Merge request reopened")
		Expect(reopened.Text).To(ContainSubstring("reopened merge request !1"))
		expectReply(reopened, assigneeFirst)
		reviewerReopened := email.waitForSubject(reviewer.email, "Merge request reopened")
		expectReply(reviewerReopened, reviewerFirst)

		Consistently(func() []int {
			return []int{
				email.countSubject(author.email, "Merge request opened"),
				email.countSubject(author.email, "Merge request ready for review"),
				email.countSubject(author.email, "Merge request reopened"),
				email.countSubject(reviewer.email, "Review submitted"),
				email.countSubject(reviewer.email, "Merge request closed"),
				email.countSubject(reviewer.email, "Review requested"),
			}
		}, "2s", "100ms").Should(Equal([]int{0, 0, 0, 0, 0, 0}), "actors are skipped and create-time reviewers only get the opened email")
		Expect(email.countSubject(reviewer.email, "Merge request opened")).To(Equal(1))
		Expect(email.countSubject(assignee.email, "Merge request opened")).To(Equal(1))
		Expect(email.countSubject(reviewer.email, "Merge request ready for review")).To(Equal(1))
		Expect(email.countSubject(assignee.email, "Merge request ready for review")).To(Equal(1))
		Expect(email.countSubject(reviewer.email, "Merge request reopened")).To(Equal(1), "a past reviewer stays a participant")
	})

	It("retries failed deliveries and redelivers them from the admin surface", func() {
		project := newProject("email-retry")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")

		author := newIdentity("email-retry-author").promote()
		reviewer := newIdentity("email-retry-reviewer").promote()
		reviewerAPI := newAPIClient(cli.apiURL, reviewer.email, reviewer.password)

		parent := workspace()
		dir := cloneRepoAs(author, parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipaAs(author, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipaAs(author, dir, "add", "feature.txt").ExitCode).To(Equal(0))
		push := runNipaAs(author, dir, "push", "-m", uniqueMessage("email retry work"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
		create := runNipaAs(author, dir, "mr", "create", "--title", uniqueMessage("Email retry MR"))
		Expect(create.ExitCode).To(Equal(0), create.Output())

		email.failFor(author.email, 2)
		reviewerAPI.addMergeRequestComment(orgSlug, project, 1, "", "retry me")

		var failed emailDeliveryJSON
		Eventually(func() bool {
			for _, delivery := range api.listEmailDeliveries(orgSlug, project, "failed").Deliveries {
				if delivery.Email == author.email {
					failed = delivery
					return true
				}
			}
			return false
		}, "20s", "100ms").Should(BeTrue(), "the delivery never reached failed")

		Expect(failed.Attempts).To(Equal(int64(2)), "both configured attempts were used")
		Expect(failed.State).To(Equal("failed"))
		Expect(failed.LastError).NotTo(BeEmpty())

		redelivered := api.redeliverEmailDelivery(orgSlug, project, failed.ID)
		Expect(redelivered.State).NotTo(Equal("failed"), "redelivery re-queues the delivery")
		email.waitForSubject(author.email, "New comment")

		Eventually(func() string {
			for _, delivery := range api.listEmailDeliveries(orgSlug, project, "delivered").Deliveries {
				if delivery.ID == failed.ID {
					return delivery.State
				}
			}
			return ""
		}, "20s", "100ms").Should(Equal("delivered"))
	})

	It("emails synchronized, status check and merge events", func() {
		project := newProject("email-events")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")

		author := newIdentity("email-events-author").promote()
		reviewer := newIdentity("email-events-reviewer").promote()

		parent := workspace()
		dir := cloneRepoAs(author, parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipaAs(author, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipaAs(author, dir, "add", "feature.txt").ExitCode).To(Equal(0))
		push := runNipaAs(author, dir, "push", "-m", uniqueMessage("email events work"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
		create := runNipaAs(author, dir, "mr", "create", "--title", uniqueMessage("Email events MR"))
		Expect(create.ExitCode).To(Equal(0), create.Output())
		request := runNipaAs(author, dir, "mr", "request-review", "1", reviewer.userID)
		Expect(request.ExitCode).To(Equal(0), request.Output())
		email.waitForSubject(reviewer.email, "Review requested")

		// A push to the source branch notifies the reviewer.
		writeText(dir, "feature.txt", "feature v2\n")
		Expect(runNipaAs(author, dir, "add", "feature.txt").ExitCode).To(Equal(0))
		pushAgain := runNipaAs(author, dir, "push", "-m", uniqueMessage("email events push"))
		Expect(pushAgain.ExitCode).To(Equal(0), pushAgain.Output())
		syncEmail := email.waitForSubject(reviewer.email, "New pushes")
		Expect(syncEmail.Text).To(ContainSubstring("pushed to merge request !1"))

		// A successful status check stays silent; a failed one notifies.
		baseline := email.countFor(reviewer.email)
		success := runNipaAs(author, dir, "mr", "check", "1", "--name", "build", "--state", "success")
		Expect(success.ExitCode).To(Equal(0), success.Output())
		Consistently(func() int { return email.countFor(reviewer.email) }, "2s", "100ms").Should(Equal(baseline))

		failed := runNipaAs(author, dir, "mr", "check", "1", "--name", "build", "--state", "failed")
		Expect(failed.ExitCode).To(Equal(0), failed.Output())
		checkEmail := email.waitForSubject(reviewer.email, "Status check failed")
		Expect(checkEmail.Text).To(ContainSubstring("reported a failed status check on merge request !1"))

		merge := runNipaAs(author, dir, "mr", "merge", "1")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())
		mergeEmail := email.waitForSubject(reviewer.email, "Merge request merged")
		Expect(mergeEmail.Text).To(ContainSubstring("merged merge request !1"))
	})
})
