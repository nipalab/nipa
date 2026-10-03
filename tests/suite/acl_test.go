package suite_test

import (
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var grantedRuleRE = regexp.MustCompile(`Granted rule (\d+)`)

func grantRule(dir string, args ...string) string {
	GinkgoHelper()
	res := runNipa(dir, append([]string{"acl", "grant"}, args...)...)
	Expect(res.ExitCode).To(Equal(0), res.Output())
	match := grantedRuleRE.FindStringSubmatch(res.Output())
	Expect(match).NotTo(BeNil(), res.Output())
	return match[1]
}

var _ = Describe("nipa acl", func() {
	It("grants and lists a user rule", func() {
		project := newProject("acl-grant")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		writer := newIdentity("acl-grant-writer")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		ruleID := grantRule(dir, "--user", writer.userID, "--permission", "read,write")

		list := runNipa(dir, "acl", "list")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring(ruleID + "\tuser:" + writer.userID + "\t/\tread,write"))
	})

	It("revokes a rule by id", func() {
		project := newProject("acl-revoke")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		writer := newIdentity("acl-revoke-writer")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		ruleID := grantRule(dir, "--user", writer.userID, "--permission", "read")

		res := runNipa(dir, "acl", "revoke", "--id", ruleID)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Deleted rule " + ruleID))

		list := runNipa(dir, "acl", "list")
		Expect(list.Output()).To(ContainSubstring("no access rules"))
	})

	It("denies clone without a rule and allows it with read", func() {
		project := newProject("acl-read")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("acl-read-writer")

		denied := runNipaAs(writer, workspace(), "clone", url, "work")
		Expect(denied.ExitCode).NotTo(Equal(0))
		Expect(denied.Output()).NotTo(BeEmpty())

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "acl", "grant", "--user", writer.userID, "--permission", "read").ExitCode).To(Equal(0))

		dir := cloneRepoAs(writer, workspace(), url, "work")
		Expect(readText(dir, "a.txt")).To(Equal("a\n"))
	})

	It("denies push without write and allows it with write", func() {
		project := newProject("acl-write")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("acl-write-writer")

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "acl", "grant", "--user", writer.userID, "--permission", "read").ExitCode).To(Equal(0))

		dir := cloneRepoAs(writer, workspace(), url, "work")
		writeText(dir, "b.txt", "b\n")
		Expect(runNipaAs(writer, dir, "add", "b.txt").ExitCode).To(Equal(0))

		denied := runNipaAs(writer, dir, "push", "-m", uniqueMessage("no write"))
		Expect(denied.ExitCode).NotTo(Equal(0))
		Expect(denied.Output()).NotTo(BeEmpty())

		Expect(runNipa(adminDir, "acl", "grant", "--user", writer.userID, "--permission", "read,write").ExitCode).To(Equal(0))

		push := runNipaAs(writer, dir, "push", "-m", uniqueMessage("with write"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
	})

	It("limits a path-scoped rule to its prefix", func() {
		project := newProject("acl-path")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("acl-path-writer")

		adminDir := cloneRepo(workspace(), url, "admin")
		grant := runNipa(adminDir, "acl", "grant", "--user", writer.userID, "--path", "src", "--permission", "read,write")
		Expect(grant.ExitCode).To(Equal(0), grant.Output())

		dir := cloneRepoAs(writer, workspace(), url, "work")
		Expect(readText(dir, "src/main.go")).To(Equal("package main\n"))
		Expect(fileExists(dir, "docs/a.txt")).To(BeFalse(), "read filter hides paths outside the rule")

		writeText(dir, "src/main.go", "package main // v2\n")
		Expect(runNipaAs(writer, dir, "add", "src/main.go").ExitCode).To(Equal(0))
		push := runNipaAs(writer, dir, "push", "-m", uniqueMessage("scoped edit"))
		Expect(push.ExitCode).To(Equal(0), push.Output())

		full := cloneRepo(workspace(), url, "full")
		Expect(readText(full, "src/main.go")).To(Equal("package main // v2\n"))
		Expect(readText(full, "docs/a.txt")).To(Equal("docs\n"))
	})

	It("shows effective permissions with acl my", func() {
		project := newProject("acl-my")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("acl-my-writer")

		adminDir := cloneRepo(workspace(), url, "admin")
		admin := runNipa(adminDir, "acl", "my")
		Expect(admin.ExitCode).To(Equal(0), admin.Output())
		Expect(admin.Output()).To(ContainSubstring("project: read,write,lock,admin"))

		Expect(runNipa(adminDir, "acl", "grant", "--user", writer.userID, "--permission", "read").ExitCode).To(Equal(0))

		dir := cloneRepoAs(writer, workspace(), url, "work")
		mine := runNipaAs(writer, dir, "acl", "my")
		Expect(mine.ExitCode).To(Equal(0), mine.Output())
		Expect(mine.Output()).To(ContainSubstring("project: read"))
		Expect(mine.Output()).To(ContainSubstring("rule\t/\tread"))
	})

	It("applies project path defaults to every user", func() {
		project := newProject("acl-defaults")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("acl-default-writer")

		adminDir := cloneRepo(workspace(), url, "admin")
		set := runNipa(adminDir, "acl", "permission", "set", "--path", "docs", "--permission", "read")
		Expect(set.ExitCode).To(Equal(0), set.Output())
		Expect(set.Output()).To(ContainSubstring("Set read default on docs"))

		list := runNipa(adminDir, "acl", "permission", "list")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("docs\tread"))

		dir := cloneRepoAs(writer, workspace(), url, "work")
		Expect(readText(dir, "docs/a.txt")).To(Equal("docs\n"))
		Expect(fileExists(dir, "src/main.go")).To(BeFalse())

		remove := runNipa(adminDir, "acl", "permission", "remove", "--path", "docs")
		Expect(remove.ExitCode).To(Equal(0), remove.Output())
		Expect(remove.Output()).To(ContainSubstring("Removed default on docs"))

		denied := runNipaAs(writer, workspace(), "clone", url, "again")
		Expect(denied.ExitCode).NotTo(Equal(0))
	})

	It("rejects invalid grants", func() {
		project := newProject("acl-errors")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		both := runNipa(dir, "acl", "grant", "--user", "1", "--group", "2", "--permission", "read")
		Expect(both.ExitCode).To(Equal(1))
		Expect(both.Output()).To(ContainSubstring("exactly one of --user or --group"))

		neither := runNipa(dir, "acl", "grant", "--permission", "read")
		Expect(neither.ExitCode).To(Equal(1))
		Expect(neither.Output()).To(ContainSubstring("exactly one of --user or --group"))

		unknown := runNipa(dir, "acl", "grant", "--user", "1", "--permission", "fly")
		Expect(unknown.ExitCode).To(Equal(1))
		Expect(unknown.Output()).To(ContainSubstring("unknown permission"))

		empty := runNipa(dir, "acl", "grant", "--user", "1", "--permission", "")
		Expect(empty.ExitCode).To(Equal(1))
		Expect(empty.Output()).NotTo(BeEmpty())
	})
})
