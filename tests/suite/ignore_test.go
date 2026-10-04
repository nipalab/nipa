package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa ignore rules", func() {
	It("hides ignored untracked files from status and skips them on add", func() {
		project := newProject("ignore-status")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaignore": []byte("*.log\nbuild/\n"),
			"a.txt":       []byte("a\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeText(dir, "app.log", "log\n")
		writeText(dir, "build/out.o", "o\n")

		res := runNipa(dir, "status")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).NotTo(ContainSubstring("app.log"))
		Expect(res.Output()).NotTo(ContainSubstring("build/out.o"))

		add := runNipa(dir, "add", "")
		Expect(add.ExitCode).To(Equal(0), add.Output())
		res = runNipa(dir, "status")
		Expect(res.Output()).NotTo(ContainSubstring("app.log"))
		Expect(res.Output()).NotTo(ContainSubstring("build/out.o"))
	})

	It("requires -f for an explicitly ignored file and tracks it after push", func() {
		project := newProject("ignore-force")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaignore": []byte("*.log\n"),
			"a.txt":       []byte("a\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "app.log", "log\n")

		res := runNipa(dir, "add", "app.log")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("ignored"))
		Expect(res.Output()).To(ContainSubstring("-f"))

		res = runNipa(dir, "add", "-f", "app.log")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		push := pushRepo(dir, "force add log")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		full := cloneRepo(parent, url, "full")
		Expect(readText(full, "app.log")).To(Equal("log\n"))

		writeText(full, "other.log", "other\n")
		res = runNipa(full, "status")
		Expect(res.Output()).NotTo(ContainSubstring("other.log"), "new files still match the rules")
	})

	It("materializes the root .nipaignore in sparse clones", func() {
		project := newProject("ignore-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaignore": []byte("*.log\n"),
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work", "--sparse", "src")

		Expect(fileExists(dir, ".nipaignore")).To(BeTrue())
		Expect(readText(dir, ".nipaignore")).To(Equal("*.log\n"))

		writeText(dir, "tmp.log", "log\n")
		res := runNipa(dir, "status")
		Expect(res.Output()).NotTo(ContainSubstring("tmp.log"))

		res = runNipa(dir, "add", "tmp.log")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("ignored"))
	})
})
