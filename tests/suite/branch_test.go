package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa branch", func() {
	It("prints the current branch", func() {
		project := newProject("branch-current")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("main"))

		var out currentBranchJSON
		runJSONInto(dir, &out, "branch", "--json")
		Expect(out.Branch).To(Equal("main"))
	})

	It("lists all branches and marks the current one", func() {
		project := newProject("branch-list")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-c", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		list := runNipa(dir, "branch", "-a")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("feature *"))
		Expect(list.Output()).To(ContainSubstring("main"))

		var out branchesJSON
		runJSONInto(dir, &out, "branch", "-a", "--json")
		Expect(out.Current).To(Equal("feature"))
		Expect(out.Branches).To(ContainElement(And(
			HaveField("Name", "feature"),
			HaveField("Current", true),
		)))
		Expect(out.Branches).To(ContainElement(And(
			HaveField("Name", "main"),
			HaveField("Current", false),
			HaveField("Default", true),
		)))
	})

	It("creates a branch that forks from the pinned head", func() {
		project := newProject("branch-create")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-c", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Created and switched to branch "feature"`))
		Expect(loadRepoConfig(dir).Branch).To(Equal("feature"))

		var out branchesJSON
		runJSONInto(dir, &out, "branch", "-a", "--json")
		mainHead, featureHead := "", ""
		for _, b := range out.Branches {
			switch b.Name {
			case "main":
				mainHead = b.CommitID
			case "feature":
				featureHead = b.CommitID
			}
		}
		Expect(mainHead).NotTo(BeEmpty())
		Expect(featureHead).To(Equal(mainHead))
	})

	It("fails to create a branch that already exists", func() {
		project := newProject("branch-duplicate")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-c", "main")
		Expect(res.ExitCode).To(Equal(2))
		Expect(res.Output()).To(ContainSubstring("already exists"))
	})

	It("deletes a branch", func() {
		project := newProject("branch-delete")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		res := runNipa(dir, "branch", "-d", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Deleted branch "feature"`))

		list := runNipa(dir, "branch", "-a")
		Expect(list.Output()).NotTo(ContainSubstring("feature"))
	})

	It("refuses to delete the default branch", func() {
		project := newProject("branch-delete-default")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))

		res := runNipa(dir, "branch", "-d", "main")
		Expect(res.ExitCode).To(Equal(2))
		Expect(res.Output()).To(ContainSubstring("cannot delete the default branch"))
	})

	It("refuses to delete a protected branch until it is unprotected", func() {
		project := newProject("branch-delete-protected")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		protection := api.setBranchProtection(orgSlug, project, "feature", true, nil, nil)
		Expect(protection.IsProtected).To(BeTrue())

		res := runNipa(dir, "branch", "-d", "feature")
		Expect(res.ExitCode).To(Equal(2))
		Expect(res.Output()).To(ContainSubstring("protected"))

		protection = api.setBranchProtection(orgSlug, project, "feature", false, nil, nil)
		Expect(protection.IsProtected).To(BeFalse())

		res = runNipa(dir, "branch", "-d", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Deleted branch "feature"`))
	})

	It("refuses to delete the current branch", func() {
		project := newProject("branch-delete-current")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-d", "main")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("cannot delete the current branch"))
	})

	It("fails to delete an unknown branch", func() {
		project := newProject("branch-delete-missing")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-d", "nope")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("rejects mutually exclusive flags", func() {
		project := newProject("branch-flags")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "branch", "-a", "-c", "feature")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("mutually exclusive"))

		res = runNipa(dir, "branch", "-c", "feature", "--json")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("--json cannot be combined"))
	})
})
