package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa switch", func() {
	It("switches branches and materializes the target tree", func() {
		project := newProject("switch-branch")
		seedRepo(orgSlug, project, map[string][]byte{"main.txt": []byte("main\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))

		res := runNipa(dir, "switch", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Switched to branch "main"`))
		Expect(readText(dir, "main.txt")).To(Equal("main\n"))
		Expect(fileExists(dir, "feature.txt")).To(BeFalse())

		cfg := loadRepoConfig(dir)
		Expect(cfg.Branch).To(Equal("main"))
		Expect(cfg.Head).To(BeNil())
		assertCleanStatus(dir)
	})

	It("reports when already on the branch", func() {
		project := newProject("switch-same")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "switch", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Already on branch "main"`))
	})

	It("refuses to switch with staged changes", func() {
		project := newProject("switch-staged")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "staged.txt", "staged\n")
		Expect(runNipa(dir, "add", "staged.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "switch", "main")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("cannot switch branches with staged changes"))
		Expect(loadRepoConfig(dir).Branch).To(Equal("feature"))
	})

	It("fails to switch to an unknown branch", func() {
		project := newProject("switch-missing")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "switch", "nope")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("detaches HEAD at a tag", func() {
		project := newProject("switch-tag")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0", "-m", "release").ExitCode).To(Equal(0))

		res := runNipa(dir, "switch", "--tag", "v1.0.0")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`HEAD detached at tag "v1.0.0"`))

		cfg := loadRepoConfig(dir)
		Expect(cfg.Branch).To(Equal("main"))
		Expect(cfg.Head).NotTo(BeNil())
		Expect(cfg.Head.Kind).To(Equal("tag"))
		Expect(cfg.Head.Name).To(Equal("v1.0.0"))

		status := runNipa(dir, "status")
		Expect(status.ExitCode).To(Equal(0), status.Output())
		Expect(status.Output()).To(ContainSubstring(`HEAD detached at tag "v1.0.0"`))
	})

	It("keeps the pinned commit on update while detached", func() {
		project := newProject("switch-tag-update")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "--tag", "v1.0.0").ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v2\n")}, "move main")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readText(dir, "a.txt")).To(Equal("v1\n"), "update must re-sync the tagged commit, not the branch head")
	})

	It("refuses push, merge and revert while detached", func() {
		project := newProject("switch-tag-refusals")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "--tag", "v1.0.0").ExitCode).To(Equal(0))

		push := runNipa(dir, "push", "-m", uniqueMessage("detached push"))
		Expect(push.ExitCode).NotTo(Equal(0))
		Expect(push.Output()).To(ContainSubstring("detached"))

		merge := runNipa(dir, "merge", "main")
		Expect(merge.ExitCode).NotTo(Equal(0))
		Expect(merge.Output()).To(ContainSubstring("detached"))

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).NotTo(BeEmpty())
		revert := runNipa(dir, "revert", log.Commits[0].ID)
		Expect(revert.ExitCode).NotTo(Equal(0))
		Expect(revert.Output()).To(ContainSubstring("detached"))
	})

	It("re-attaches and forks from the pinned commit on branch create", func() {
		project := newProject("switch-tag-branch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "--tag", "v1.0.0").ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("v2\n")}, "move main")

		res := runNipa(dir, "branch", "-c", "from-tag")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		cfg := loadRepoConfig(dir)
		Expect(cfg.Branch).To(Equal("from-tag"))
		Expect(cfg.Head).To(BeNil())
		Expect(fileExists(dir, "b.txt")).To(BeFalse(), "branch must fork from the tagged commit")

		var out branchesJSON
		runJSONInto(dir, &out, "branch", "-a", "--json")
		for _, b := range out.Branches {
			if b.Name == "from-tag" {
				Expect(b.CommitID).NotTo(BeEmpty())
			}
		}
	})

	It("clears the detached marker when switching back to a branch", func() {
		project := newProject("switch-tag-back")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "--tag", "v1.0.0").ExitCode).To(Equal(0))

		res := runNipa(dir, "switch", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(loadRepoConfig(dir).Head).To(BeNil())
		assertCleanStatus(dir)
	})

	It("rejects invalid flag combinations", func() {
		project := newProject("switch-flags")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "switch", "--tag", "v1", "main")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("--tag cannot be combined"))

		res = runNipa(dir, "switch")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("branch name is required"))
	})
})
