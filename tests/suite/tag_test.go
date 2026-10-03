package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa tag", func() {
	It("lists an empty tag list", func() {
		project := newProject("tag-empty")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "tag")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("no tags"))

		var out tagsJSON
		runJSONInto(dir, &out, "tag", "--json")
		Expect(out.Tags).To(BeEmpty())
	})

	It("creates a tag at the checked-out commit", func() {
		project := newProject("tag-create")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		head := log.Commits[0].ID

		res := runNipa(dir, "tag", "-c", "v1.0.0")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Created tag "v1.0.0"`))

		list := runNipa(dir, "tag")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("v1.0.0"))
		Expect(list.Output()).To(ContainSubstring(head))

		var out tagsJSON
		runJSONInto(dir, &out, "tag", "--json")
		Expect(out.Tags).To(HaveLen(1))
		Expect(out.Tags[0].Name).To(Equal("v1.0.0"))
		Expect(out.Tags[0].CommitID).To(Equal(head))
		Expect(out.Tags[0].CreatedAt).NotTo(BeEmpty())
	})

	It("stores an annotation message", func() {
		project := newProject("tag-message")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "tag", "-c", "v1.0.0", "-m", "first release").ExitCode).To(Equal(0))

		var out tagsJSON
		runJSONInto(dir, &out, "tag", "--json")
		Expect(out.Tags).To(HaveLen(1))
		Expect(out.Tags[0].Message).To(Equal("first release"))

		list := runNipa(dir, "tag")
		Expect(list.Output()).To(ContainSubstring("first release"))
	})

	It("rejects a duplicate tag name", func() {
		project := newProject("tag-duplicate")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))

		res := runNipa(dir, "tag", "-c", "v1.0.0")
		Expect(res.ExitCode).To(Equal(2))
		Expect(res.Output()).To(ContainSubstring("already exists"))
	})

	It("tags a branch head with --branch", func() {
		project := newProject("tag-branch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		var branches branchesJSON
		runJSONInto(dir, &branches, "branch", "-a", "--json")
		featureHead := ""
		for _, b := range branches.Branches {
			if b.Name == "feature" {
				featureHead = b.CommitID
			}
		}
		Expect(featureHead).NotTo(BeEmpty())

		res := runNipa(dir, "tag", "-c", "v2.0.0", "--branch", "feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		var out tagsJSON
		runJSONInto(dir, &out, "tag", "--json")
		Expect(out.Tags).To(HaveLen(1))
		Expect(out.Tags[0].CommitID).To(Equal(featureHead))
	})

	It("tags an explicit commit by id and by hash", func() {
		project := newProject("tag-commit")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(2))
		first := log.Commits[1]

		byID := runNipa(dir, "tag", "-c", "v1.0.0", "--commit", first.ID)
		Expect(byID.ExitCode).To(Equal(0), byID.Output())
		byHash := runNipa(dir, "tag", "-c", "v2.0.0", "--commit", first.Hash)
		Expect(byHash.ExitCode).To(Equal(0), byHash.Output())

		var out tagsJSON
		runJSONInto(dir, &out, "tag", "--json")
		Expect(out.Tags).To(HaveLen(2))
		for _, tag := range out.Tags {
			Expect(tag.CommitID).To(Equal(first.ID))
		}
	})

	It("deletes a tag and frees the name", func() {
		project := newProject("tag-delete")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		res := runNipa(dir, "tag", "-d", "v1.0.0")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`Deleted tag "v1.0.0"`))

		list := runNipa(dir, "tag")
		Expect(list.Output()).To(ContainSubstring("no tags"))

		recreate := runNipa(dir, "tag", "-c", "v1.0.0")
		Expect(recreate.ExitCode).To(Equal(0), recreate.Output())
	})

	It("fails to delete an unknown tag", func() {
		project := newProject("tag-delete-missing")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "tag", "-d", "nope")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("rejects invalid flag combinations", func() {
		project := newProject("tag-flags")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "tag", "-c", "-d", "v1.0.0")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("mutually exclusive"))

		res = runNipa(dir, "tag", "-m", "note", "v1.0.0")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("require --create"))

		res = runNipa(dir, "tag", "v1.0.0")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("to create a tag use"))

		res = runNipa(dir, "tag", "-c", "v1.0.0", "--json")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("--json cannot be combined"))
	})
})
