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
		Expect(reviewEmail.MessageID).To(ContainSubstring("-mr1@"), "the first contact carries the thread root")
		Expect(reviewEmail.InReplyTo).To(BeEmpty())
		Expect(email.countFor(author.email)).To(Equal(0), "the requesting author is not emailed")

		reviewerAPI.addMergeRequestComment(orgSlug, project, 1, "", "looks good")
		commentEmail := email.waitForSubject(author.email, "New comment")
		Expect(commentEmail.Text).To(ContainSubstring("commented on merge request !1"))
		Expect(commentEmail.MessageID).To(ContainSubstring("-mr1@"))
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
})
