package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa merge", func() {
	It("fast-forwards when the target branch has not moved", func() {
		project := newProject("merge-ff")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		res := runNipa(dir, "merge", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Fast-forwarded current branch to "feature"`))
		Expect(readText(dir, "feature.txt")).To(Equal("feature\n"))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits[0].ParentIDs).To(HaveLen(1))
	})

	It("reports already up to date", func() {
		project := newProject("merge-uptodate")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "merge", "feature").ExitCode).To(Equal(0))

		res := runNipa(dir, "merge", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Already up to date"))
	})

	It("creates a merge commit with --no-ff", func() {
		project := newProject("merge-no-ff")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		message := uniqueMessage("merge feature")
		res := runNipa(dir, "merge", "--no-ff", "-m", message, "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Merge committed"))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits[0].ParentIDs).To(HaveLen(2))
		Expect(out.Commits[0].Message).To(Equal(message))
		Expect(readText(dir, "feature.txt")).To(Equal("feature\n"))
	})

	It("three-way merges non-conflicting changes", func() {
		project := newProject("merge-threeway")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature change").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "main.txt", "main\n")
		Expect(runNipa(dir, "add", "main.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main change").ExitCode).To(Equal(0))

		res := runNipa(dir, "merge", "-m", uniqueMessage("merge feature"), "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Merge committed"))
		Expect(readText(dir, "feature.txt")).To(Equal("feature\n"))
		Expect(readText(dir, "main.txt")).To(Equal("main\n"))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits[0].ParentIDs).To(HaveLen(2))
	})

	It("stops on conflicts and leaves merge markers", func() {
		project := newProject("merge-conflict")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "file.txt", "feature\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature edit").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "file.txt", "main\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main edit").ExitCode).To(Equal(0))

		res := runNipa(dir, "merge", "feature")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("conflict"))
		Expect(readText(dir, "file.txt")).To(ContainSubstring("<<<<<<<"))
		Expect(readText(dir, "file.txt")).To(ContainSubstring(">>>>>>>"))

		status := runNipa(dir, "status")
		Expect(status.Output()).To(ContainSubstring("C  file.txt"))

		abort := runNipa(dir, "merge", "--abort")
		Expect(abort.ExitCode).To(Equal(0), abort.Output())
		Expect(abort.Output()).To(ContainSubstring("Merge aborted"))
		Expect(readText(dir, "file.txt")).To(Equal("main\n"))
		assertCleanStatus(dir)
	})

	It("completes a conflicted merge when resolved and pushed", func() {
		project := newProject("merge-resolve")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "file.txt", "feature\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature edit").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "file.txt", "main\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main edit").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "merge", "feature").ExitCode).NotTo(Equal(0))
		writeText(dir, "file.txt", "resolved\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))

		push := pushRepo(dir, "resolve merge")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "file.txt")).To(Equal("resolved\n"))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits[0].ParentIDs).To(HaveLen(2))
	})

	It("refuses --ff-only when the branches diverged", func() {
		project := newProject("merge-ff-only")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature change").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "main.txt", "main\n")
		Expect(runNipa(dir, "add", "main.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main change").ExitCode).To(Equal(0))

		res := runNipa(dir, "merge", "--ff-only", "feature")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("cannot fast-forward"))
	})

	It("prints the plan with --dry-run", func() {
		project := newProject("merge-dry-run")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature change").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "main.txt", "main\n")
		Expect(runNipa(dir, "add", "main.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main change").ExitCode).To(Equal(0))

		res := runNipa(dir, "merge", "--dry-run", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("would merge"))

		var plan planJSON
		runJSONInto(dir, &plan, "merge", "--dry-run", "--json", "feature")
		Expect(plan.Kind).To(Equal("merge"))
		Expect(plan.UpToDate).To(BeFalse())
		Expect(plan.Changes).NotTo(BeEmpty())

		var same planJSON
		runJSONInto(dir, &same, "merge", "--dry-run", "--json", "main")
		Expect(same.UpToDate).To(BeTrue())
	})

	It("refuses to merge in a sparse clone", func() {
		project := newProject("merge-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work", "--sparse", "src")
		res := runNipa(dir, "merge", "main")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("sparse or subdirectory clone is not supported"))
	})

	It("fails to merge an unknown branch", func() {
		project := newProject("merge-unknown")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "merge", "nope")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("requires a source branch unless aborting", func() {
		project := newProject("merge-args")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "merge")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("source branch to merge is required"))
	})
})
