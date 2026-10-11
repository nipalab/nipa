package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa restore", func() {
	It("discards modifications to a file", func() {
		project := newProject("restore-file")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))

		writeText(dir, "file.txt", "local edit\n")
		res := runNipa(dir, "restore", "file.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))
		assertCleanStatus(dir)

		Expect(os.Chmod(filepath.Join(dir, "file.txt"), 0o755)).To(Succeed())
		res = runNipa(dir, "restore", "file.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		assertCleanStatus(dir)

		info, err := os.Stat(filepath.Join(dir, "file.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o644)), "a mode-only change is discarded too")
	})

	It("restores a deleted tracked file", func() {
		project := newProject("restore-deleted")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("keep me\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		removePath(dir, "file.txt")
		Expect(fileExists(dir, "file.txt")).To(BeFalse())

		res := runNipa(dir, "restore", "file.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "file.txt")).To(Equal("keep me\n"))
		assertCleanStatus(dir)
	})

	It("restores every file under a directory", func() {
		project := newProject("restore-dir")
		seedRepo(orgSlug, project, map[string][]byte{
			"docs/a.txt":     []byte("a\n"),
			"docs/sub/b.txt": []byte("b\n"),
			"other.txt":      []byte("other\n"),
		}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "docs/a.txt", "edit a\n")
		writeText(dir, "docs/sub/b.txt", "edit b\n")
		writeText(dir, "other.txt", "keep\n")

		res := runNipa(dir, "restore", "docs")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "docs/a.txt")).To(Equal("a\n"))
		Expect(readText(dir, "docs/sub/b.txt")).To(Equal("b\n"))
		Expect(readText(dir, "other.txt")).To(Equal("keep\n"), "paths outside the target stay modified")

		writeText(dir, "docs/a.txt", "edited again\n")
		res = runNipa(dir, "restore", "docs/")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "docs/a.txt")).To(Equal("a\n"), "a trailing slash is accepted like shell tab-completion")

		status := runNipa(dir, "status")
		Expect(status.Output()).To(ContainSubstring("M  other.txt"))
	})

	It("refuses staged files", func() {
		project := newProject("restore-staged")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "file.txt", "staged edit\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "restore", "file.txt")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("staged changes"))
		Expect(readText(dir, "file.txt")).To(Equal("staged edit\n"), "a refused restore must not touch the file")
	})

	It("rejects untracked paths", func() {
		project := newProject("restore-untracked")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "local-notes.txt", "mine\n")

		res := runNipa(dir, "restore", "local-notes.txt")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("not tracked"))
		Expect(readText(dir, "local-notes.txt")).To(Equal("mine\n"))
	})

	It("works from a subdirectory", func() {
		project := newProject("restore-subdir")
		seedRepo(orgSlug, project, map[string][]byte{"src/deep/file.txt": []byte("v1\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "src/deep/file.txt", "local edit\n")

		res := runNipa(dir+"/src/deep", "restore", "src/deep/file.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "src/deep/file.txt")).To(Equal("v1\n"))
		assertCleanStatus(dir)
	})
})
