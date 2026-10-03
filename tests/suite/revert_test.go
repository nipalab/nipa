package suite_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa revert", func() {
	It("reverts a single commit forward-only", func() {
		project := newProject("revert-single")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		target := before.Commits[0].ID

		res := runNipa(dir, "revert", target)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Revert committed"))
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))

		var after logJSON
		runJSONInto(dir, &after, "log", "--json")
		Expect(after.Commits).To(HaveLen(3))
		Expect(after.Commits[0].ParentIDs).To(Equal([]string{target}))
		Expect(after.Commits[0].Message).To(ContainSubstring("Revert"))
	})

	It("uses -m for the revert commit message", func() {
		project := newProject("revert-message")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		message := uniqueMessage("custom revert")

		res := runNipa(dir, "revert", "-m", message, before.Commits[0].ID)
		Expect(res.ExitCode).To(Equal(0), res.Output())

		var after logJSON
		runJSONInto(dir, &after, "log", "--json")
		Expect(after.Commits[0].Message).To(Equal(message))
	})

	It("stages without committing with --no-commit", func() {
		project := newProject("revert-no-commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "second commit")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		target := before.Commits[0].ID

		res := runNipa(dir, "revert", "--no-commit", target)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("changes are staged"))
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))

		var after logJSON
		runJSONInto(dir, &after, "log", "--json")
		Expect(after.Commits).To(HaveLen(2), "no commit is created")

		push := pushRepo(dir, "commit the revert")
		Expect(push.ExitCode).To(Equal(0), push.Output())
		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "file.txt")).To(Equal("v1\n"))
	})

	It("prints the plan with --dry-run", func() {
		project := newProject("revert-dry-run")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		target := before.Commits[0].ID

		res := runNipa(dir, "revert", "--dry-run", target)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("would revert"))

		var plan planJSON
		runJSONInto(dir, &plan, "revert", "--dry-run", "--json", target)
		Expect(plan.Kind).To(Equal("revert"))
		Expect(plan.Changes).NotTo(BeEmpty())

		Expect(readText(dir, "file.txt")).To(Equal("v2\n"), "dry-run must not touch the working copy")
	})

	It("reverts a range newest-first", func() {
		project := newProject("revert-range")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "second commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v3\n")}, "third commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		Expect(before.Commits).To(HaveLen(3))
		oldest, newest := before.Commits[2].ID, before.Commits[0].ID

		res := runNipa(dir, "revert", oldest+".."+newest)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))

		var after logJSON
		runJSONInto(dir, &after, "log", "--json")
		Expect(after.Commits).To(HaveLen(5), "one revert commit per target")
	})

	It("resolves a conflict with --continue", func() {
		project := newProject("revert-continue")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("base\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("two\n")}, "second commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("three\n")}, "third commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		target := before.Commits[1].ID

		conflict := runNipa(dir, "revert", target)
		Expect(conflict.ExitCode).NotTo(Equal(0))
		Expect(conflict.Output()).To(ContainSubstring("conflict"))
		Expect(readText(dir, "file.txt")).To(ContainSubstring("<<<<<<<"))

		writeText(dir, "file.txt", "resolved\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "revert", "--continue")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Revert committed"))
		Expect(readText(dir, "file.txt")).To(Equal("resolved\n"))
		assertCleanStatus(dir)
	})

	It("aborts a conflicted revert", func() {
		project := newProject("revert-abort")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("base\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("two\n")}, "second commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("three\n")}, "third commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		target := before.Commits[1].ID

		Expect(runNipa(dir, "revert", target).ExitCode).NotTo(Equal(0))

		res := runNipa(dir, "revert", "--abort")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Revert aborted"))
		Expect(readText(dir, "file.txt")).To(Equal("three\n"))
		assertCleanStatus(dir)
	})

	It("skips a conflicted step", func() {
		project := newProject("revert-skip")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("base\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("two\n")}, "second commit")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("three\n")}, "third commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		target := before.Commits[1].ID

		Expect(runNipa(dir, "revert", target).ExitCode).NotTo(Equal(0))

		res := runNipa(dir, "revert", "--skip")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Revert step skipped"))

		var after logJSON
		runJSONInto(dir, &after, "log", "--json")
		Expect(after.Commits).To(HaveLen(3), "skipping must not create a commit")
	})

	It("requires --mainline for a merge commit and reverts with it", func() {
		project := newProject("revert-mainline")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature change").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "merge", "--no-ff", "-m", uniqueMessage("merge feature"), "feature").ExitCode).To(Equal(0))

		var before logJSON
		runJSONInto(dir, &before, "log", "--json")
		mergeID := before.Commits[0].ID
		Expect(before.Commits[0].ParentIDs).To(HaveLen(2))

		missing := runNipa(dir, "revert", mergeID)
		Expect(missing.ExitCode).NotTo(Equal(0))
		Expect(missing.Output()).To(ContainSubstring("mainline"))

		res := runNipa(dir, "revert", "--mainline", "1", mergeID)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Revert committed"))
		Expect(fileExists(dir, "feature.txt")).To(BeFalse())
	})

	It("fails on an unknown commit", func() {
		project := newProject("revert-unknown")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "revert", "1")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("rejects ranges over 16 commits", func() {
		project := newProject("revert-too-large")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		for i := 0; i < 18; i++ {
			writeText(dir, "f.txt", fmt.Sprintf("v%d\n", i))
			Expect(runNipa(dir, "add", "f.txt").ExitCode).To(Equal(0))
			Expect(pushRepo(dir, fmt.Sprintf("commit %d", i)).ExitCode).To(Equal(0))
		}

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(18))
		oldest, newest := log.Commits[17].ID, log.Commits[0].ID

		res := runNipa(dir, "revert", oldest+".."+newest)
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("revert range is too large"))
	})

	It("refuses to revert in a sparse clone", func() {
		project := newProject("revert-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work", "--sparse", "src")

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).NotTo(BeEmpty())

		res := runNipa(dir, "revert", log.Commits[0].ID)
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("sparse or subdirectory clone is not supported"))
	})
})
