package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("organization owner project access", func() {
	It("lets a non-admin owner use the project they create while plain members stay denied", func() {
		owner := newIdentity("org-owner")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)

		org := "owner-org-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		ownerAPI.createProject(org, "game", "game")
		url := repoURLFor(org, "game")

		// Clone and push as the owner exercise project read and write without
		// any PBAC rule: org ownership grants the full mask.
		dir := cloneRepoAs(owner, workspace(), url, "work")
		writeText(dir, "a.txt", "hello\n")
		Expect(runNipaAs(owner, dir, "add", "a.txt").ExitCode).To(Equal(0))
		push := runNipaAs(owner, dir, "push", "-m", uniqueMessage("owner push"))
		Expect(push.ExitCode).To(Equal(0), push.Output())

		mine := runNipaAs(owner, dir, "acl", "my")
		Expect(mine.ExitCode).To(Equal(0), mine.Output())
		Expect(mine.Output()).To(ContainSubstring("project: read,write,lock,admin"))

		// A plain org member still needs PBAC rules.
		member := newIdentity("org-member")
		ownerAPI.addOrgMember(org, member.email, "member")
		denied := runNipaAs(member, workspace(), "clone", url, "member-work")
		Expect(denied.ExitCode).NotTo(Equal(0))
		Expect(denied.Output()).NotTo(BeEmpty())
	})
})
