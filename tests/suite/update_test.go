package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa update", func() {
	It("updates to the latest head", func() {
		project := newProject("update-basic")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))

		seedRepo(orgSlug, project, map[string][]byte{
			"file.txt": []byte("v2\n"),
			"new.txt":  []byte("new\n"),
		}, "remote update")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "file.txt")).To(Equal("v2\n"))
		Expect(readText(dir, "new.txt")).To(Equal("new\n"))
		assertCleanStatus(dir)
	})

	It("is a no-op when already up to date", func() {
		project := newProject("update-noop")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		before, err := os.Stat(filepath.Join(dir, "file.txt"))
		Expect(err).NotTo(HaveOccurred())

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)

		after, err := os.Stat(filepath.Join(dir, "file.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(after.ModTime()).To(Equal(before.ModTime()))
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))
	})

	It("removes files deleted on the server", func() {
		project := newProject("update-delete")
		seedRepo(orgSlug, project, map[string][]byte{
			"a.txt": []byte("a\n"),
			"b.txt": []byte("b\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(readText(dir, "a.txt")).To(Equal("a\n"))

		seedRepoDelete(orgSlug, project, []string{"a.txt"}, "delete a")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(fileExists(dir, "a.txt")).To(BeFalse())
		Expect(readText(dir, "b.txt")).To(Equal("b\n"))
	})

	It("preserves untracked files", func() {
		project := newProject("update-untracked")
		seedRepo(orgSlug, project, map[string][]byte{"tracked.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "local-notes.txt", "do not lose me\n")

		seedRepo(orgSlug, project, map[string][]byte{"tracked.txt": []byte("v2\n")}, "remote update")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "tracked.txt")).To(Equal("v2\n"))
		Expect(readText(dir, "local-notes.txt")).To(Equal("do not lose me\n"))
	})

	It("overwrites locally modified tracked files", func() {
		project := newProject("update-overwrite")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "file.txt", "local edit\n")

		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "remote update")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "file.txt")).To(Equal("v2\n"))
	})

	It("works from a subdirectory", func() {
		project := newProject("update-subdir")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/deep/file.txt": []byte("v1\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		seedRepo(orgSlug, project, map[string][]byte{
			"src/deep/file.txt": []byte("v2\n"),
		}, "remote update")

		res := runNipa(filepath.Join(dir, "src", "deep"), "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "src/deep/file.txt")).To(Equal("v2\n"))
	})
})
