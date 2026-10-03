package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa sparse-checkout", func() {
	It("lists the sparse state", func() {
		project := newProject("sparse-list")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "sparse-checkout", "list")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("sparse checkout disabled"))

		sparse := cloneRepo(parent, repoURLFor(orgSlug, project), "sparse", "--sparse", "src")
		res = runNipa(sparse, "sparse-checkout", "list")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("src"))
		Expect(res.Output()).NotTo(ContainSubstring("docs"))
	})

	It("replaces the sparse set with set and removes files outside it", func() {
		project := newProject("sparse-set")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		res := runNipa(dir, "sparse-checkout", "set", "src")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Sparse checkout set to 1 path(s)"))
		Expect(readText(dir, "src/main.go")).To(Equal("package main\n"))
		Expect(fileExists(dir, "docs/a.txt")).To(BeFalse())
		Expect(loadRepoConfig(dir).Sparse).To(Equal([]string{"src"}))
		assertCleanStatus(dir)

		full := cloneRepo(parent, url, "full")
		Expect(readText(full, "docs/a.txt")).To(Equal("docs\n"), "the server keeps excluded files")
	})

	It("adds and removes sparse prefixes", func() {
		project := newProject("sparse-add-remove")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work", "--sparse", "src")
		Expect(fileExists(dir, "docs/a.txt")).To(BeFalse())

		add := runNipa(dir, "sparse-checkout", "add", "docs")
		Expect(add.ExitCode).To(Equal(0), add.Output())
		Expect(add.Output()).To(ContainSubstring("Added 1 path(s)"))
		Expect(readText(dir, "docs/a.txt")).To(Equal("docs\n"))
		Expect(loadRepoConfig(dir).Sparse).To(Equal([]string{"src", "docs"}))

		remove := runNipa(dir, "sparse-checkout", "remove", "docs")
		Expect(remove.ExitCode).To(Equal(0), remove.Output())
		Expect(remove.Output()).To(ContainSubstring("Removed 1 path(s)"))
		Expect(fileExists(dir, "docs/a.txt")).To(BeFalse())
		Expect(loadRepoConfig(dir).Sparse).To(Equal([]string{"src"}))
	})

	It("disables sparse checkout and restores the full working copy", func() {
		project := newProject("sparse-disable")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work", "--sparse", "src")

		res := runNipa(dir, "sparse-checkout", "disable")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Sparse checkout disabled"))
		Expect(readText(dir, "docs/a.txt")).To(Equal("docs\n"))
		Expect(loadRepoConfig(dir).Sparse).To(BeEmpty())
		assertCleanStatus(dir)
	})

	It("pushes from a sparse clone against the full server tree", func() {
		project := newProject("sparse-push")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work", "--sparse", "src")

		writeText(dir, "src/main.go", "package main // v2\n")
		Expect(runNipa(dir, "add", "src/main.go").ExitCode).To(Equal(0))
		push := pushRepo(dir, "sparse edit")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		full := cloneRepo(parent, url, "full")
		Expect(readText(full, "src/main.go")).To(Equal("package main // v2\n"))
		Expect(readText(full, "docs/a.txt")).To(Equal("docs\n"))
	})

	It("updates only the sparse set", func() {
		project := newProject("sparse-update")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work", "--sparse", "src")

		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main // updated\n"),
			"docs/b.txt":  []byte("new docs\n"),
		}, "remote update")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readText(dir, "src/main.go")).To(Equal("package main // updated\n"))
		Expect(fileExists(dir, "docs/b.txt")).To(BeFalse())
	})

	It("requires at least one path for set", func() {
		project := newProject("sparse-set-empty")
		seedRepo(orgSlug, project, map[string][]byte{"src/main.go": []byte("package main\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "sparse-checkout", "set")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).NotTo(BeEmpty())
	})
})
