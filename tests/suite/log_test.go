package suite_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa log", func() {
	It("shows the history newest first", func() {
		project := newProject("log-order")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "log")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("first commit"))
		Expect(res.Stdout).To(ContainSubstring("second commit"))
		Expect(strings.Index(res.Stdout, "second commit")).To(BeNumerically("<", strings.Index(res.Stdout, "first commit")))
	})

	It("limits the number of commits with -n", func() {
		project := newProject("log-limit")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "log", "-n", "1")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("second commit"))
		Expect(res.Stdout).NotTo(ContainSubstring("first commit"))
	})

	It("prints one line per commit with --oneline", func() {
		project := newProject("log-oneline")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "log", "--oneline")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("second commit"))
		Expect(res.Stdout).NotTo(ContainSubstring("Author:"))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		for _, commit := range out.Commits {
			Expect(res.Stdout).To(ContainSubstring(commit.ID))
		}
	})

	It("returns the history as JSON", func() {
		project := newProject("log-json")
		firstMsg := seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		secondMsg := seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits).To(HaveLen(2))
		Expect(out.Commits[0].Message).To(Equal(secondMsg))
		Expect(out.Commits[1].Message).To(Equal(firstMsg))
		Expect(out.Commits[0].ID).NotTo(BeEmpty())
		Expect(out.Commits[0].Hash).To(HaveLen(64))
		Expect(out.Commits[0].ParentIDs).To(Equal([]string{out.Commits[1].ID}))
		Expect(out.Commits[1].ParentIDs).To(BeEmpty())
		Expect(out.Commits[0].AuthorName).NotTo(BeEmpty())
		Expect(out.Commits[0].CreatedAt).NotTo(BeEmpty())
	})

	It("prints nothing for a project without commits", func() {
		project := newProject("log-empty")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "log")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(BeEmpty())
	})

	It("walks from the pinned commit while detached", func() {
		project := newProject("log-detached")
		firstMsg := seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		Expect(runNipa(dir, "switch", "--tag", "v1.0.0").ExitCode).To(Equal(0))

		var out logJSON
		runJSONInto(dir, &out, "log", "--json")
		Expect(out.Commits).To(HaveLen(1))
		Expect(out.Commits[0].Message).To(Equal(firstMsg))
	})
})
