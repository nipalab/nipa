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
		Expect(res.Output()).To(ContainSubstring("Already up to date."))
	})

	It("leaves a deleted tracked file alone when up to date and restores it with --force", func() {
		project := newProject("update-noop-deleted")
		seedRepo(orgSlug, project, map[string][]byte{
			"file.txt":  []byte("v1\n"),
			"other.txt": []byte("o1\n"),
		}, "seed")

		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(os.Remove(filepath.Join(dir, "file.txt"))).To(Succeed())

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Already up to date."))
		Expect(fileExists(dir, "file.txt")).To(BeFalse(), "an up-to-date update does not scan the working copy")

		res = runNipa(dir, "update", "--force")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).NotTo(ContainSubstring("Already up to date."))
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))
		Expect(readText(dir, "other.txt")).To(Equal("o1\n"))
		assertCleanStatus(dir)
	})

	It("is a no-op right after a push and picks up later commits", func() {
		project := newProject("update-noop-push")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")

		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "mine.txt", "mine\n")
		Expect(runNipa(dir, "add", "mine.txt").ExitCode).To(Equal(0))
		push := runNipa(dir, "push", "-m", "add mine")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Already up to date."))

		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n")}, "remote update")
		res = runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).NotTo(ContainSubstring("Already up to date."))
		Expect(readText(dir, "file.txt")).To(Equal("v2\n"))
		Expect(readText(dir, "mine.txt")).To(Equal("mine\n"))
		assertCleanStatus(dir)
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

	It("refuses to overwrite locally modified tracked files unless forced", func() {
		project := newProject("update-overwrite")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n"), "other.txt": []byte("o1\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "file.txt", "local edit\n")

		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2\n"), "other.txt": []byte("o2\n")}, "remote update")

		res := runNipa(dir, "update")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("would be overwritten"))
		Expect(res.Output()).To(ContainSubstring("file.txt"))
		Expect(readText(dir, "file.txt")).To(Equal("local edit\n"))
		Expect(readText(dir, "other.txt")).To(Equal("o1\n"), "a refused update changes nothing")

		res = runNipa(dir, "update", "--force")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "file.txt")).To(Equal("v2\n"))
		Expect(readText(dir, "other.txt")).To(Equal("o2\n"))
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

	It("refuses to write through a symlinked folder, even forced", func() {
		project := newProject("update-symlink")
		seedRepo(orgSlug, project, map[string][]byte{
			"assets/a.txt": []byte("v1\n"),
			"top.txt":      []byte("top v1\n"),
		}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		outside := filepath.Join(parent, "outside")
		Expect(os.Mkdir(outside, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(outside, "a.txt"), []byte("v1\n"), 0o644)).To(Succeed())
		Expect(os.RemoveAll(filepath.Join(dir, "assets"))).To(Succeed())
		if err := os.Symlink(outside, filepath.Join(dir, "assets")); err != nil {
			Skip("symlinks unavailable: " + err.Error())
		}

		seedRepo(orgSlug, project, map[string][]byte{
			"assets/a.txt":   []byte("v2\n"),
			"assets/new.bin": append([]byte{0x00}, randomBytes(1024)...),
			"top.txt":        []byte("top v2\n"),
		}, "remote update")

		for _, args := range [][]string{{"update"}, {"update", "--force"}} {
			res := runNipa(dir, args...)
			Expect(res.ExitCode).NotTo(Equal(0), res.Output())
			Expect(res.Output()).To(MatchRegexp(`refusing to write "assets/[a-z.]+": "assets" is a symbolic link`))
			Expect(readText(dir, "top.txt")).To(Equal("top v1\n"), "nothing is written when the sync is refused")
			entries, err := os.ReadDir(outside)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			Expect(readText(outside, "a.txt")).To(Equal("v1\n"), "the link target is untouched")
		}
	})
})
