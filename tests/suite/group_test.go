package suite_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa group", func() {
	It("creates and lists groups", func() {
		project := newProject("group-create")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "group", "create", "developers", "--description", "the devs")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Created group developers ("))

		list := runNipa(dir, "group", "list")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("developers"))
		Expect(list.Output()).To(ContainSubstring("the devs"))
	})

	It("adds and removes members", func() {
		project := newProject("group-members")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		writer := newIdentity("group-writer")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "group", "create", "reviewers").ExitCode).To(Equal(0))
		groupID := groupIDFromList(dir, "reviewers")

		add := runNipa(dir, "group", "add-member", "--group", groupID, "--user", writer.userID)
		Expect(add.ExitCode).To(Equal(0), add.Output())
		Expect(add.Output()).To(ContainSubstring("Added user " + writer.userID))

		remove := runNipa(dir, "group", "remove-member", "--group", groupID, "--user", writer.userID)
		Expect(remove.ExitCode).To(Equal(0), remove.Output())
		Expect(remove.Output()).To(ContainSubstring("Removed user " + writer.userID))
	})

	It("grants access through group membership", func() {
		project := newProject("group-access")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("group-access-writer")

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "group", "create", "readers").ExitCode).To(Equal(0))
		groupID := groupIDFromList(adminDir, "readers")
		Expect(runNipa(adminDir, "group", "add-member", "--group", groupID, "--user", writer.userID).ExitCode).To(Equal(0))
		grant := runNipa(adminDir, "acl", "grant", "--group", groupID, "--permission", "read,write")
		Expect(grant.ExitCode).To(Equal(0), grant.Output())

		dir := cloneRepoAs(writer, workspace(), url, "work")
		writeText(dir, "b.txt", "b\n")
		Expect(runNipaAs(writer, dir, "add", "b.txt").ExitCode).To(Equal(0))
		push := runNipaAs(writer, dir, "push", "-m", uniqueMessage("group access"))
		Expect(push.ExitCode).To(Equal(0), push.Output())

		Expect(runNipa(adminDir, "group", "remove-member", "--group", groupID, "--user", writer.userID).ExitCode).To(Equal(0))

		writeText(dir, "c.txt", "c\n")
		Expect(runNipaAs(writer, dir, "add", "c.txt").ExitCode).To(Equal(0))
		denied := runNipaAs(writer, dir, "push", "-m", uniqueMessage("after removal"))
		Expect(denied.ExitCode).NotTo(Equal(0))
		Expect(denied.Output()).NotTo(BeEmpty())
	})

	It("requires organization admin rights to create groups", func() {
		project := newProject("group-permission")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("group-outsider")

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "acl", "grant", "--user", writer.userID, "--permission", "read,write").ExitCode).To(Equal(0))

		dir := cloneRepoAs(writer, workspace(), url, "work")
		res := runNipaAs(writer, dir, "group", "create", "nope")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("requires member flags", func() {
		project := newProject("group-flags")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "group", "add-member", "--group", "1")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).NotTo(BeEmpty())
	})
})

func groupIDFromList(dir, name string) string {
	GinkgoHelper()
	res := runNipa(dir, "group", "list")
	Expect(res.ExitCode).To(Equal(0), res.Output())
	for _, line := range strings.Split(strings.TrimRight(res.Output(), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 && fields[1] == name {
			return fields[0]
		}
	}
	Fail("group " + name + " not found in: " + res.Output())
	return ""
}
