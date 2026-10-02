package suite_test

import (
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa push", func() {
	It("pushes a new text file", func() {
		project := newProject("push-new")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "hello.txt", "hello from the client\n")
		res := runNipa(dir, "add", "hello.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)

		push := pushRepo(dir, "add hello")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "hello.txt")).To(Equal("hello from the client\n"))
	})

	It("pushes multiple files in nested directories", func() {
		project := newProject("push-multi")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		files := map[string]string{
			"a.txt":          "a\n",
			"src/b.go":       "package src\n",
			"src/deep/c.txt": "c\n",
			"docs/readme.md": "# readme\n",
		}
		paths := make([]string, 0, len(files))
		for path, content := range files {
			writeText(dir, path, content)
			paths = append(paths, path)
		}
		sort.Strings(paths)
		res := runNipa(dir, append([]string{"add"}, paths...)...)
		Expect(res.ExitCode).To(Equal(0), res.Stderr)

		push := pushRepo(dir, "add multiple files")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		for path, content := range files {
			Expect(readText(fresh, path)).To(Equal(content), path)
		}
	})

	It("pushes an update to an existing file", func() {
		project := newProject("push-update")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(readText(dir, "file.txt")).To(Equal("v1\n"))

		writeText(dir, "file.txt", "v2\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))
		push := pushRepo(dir, "update file")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "file.txt")).To(Equal("v2\n"))
	})

	It("fails when nothing is staged", func() {
		project := newProject("push-nothing")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := pushRepo(dir, "nothing")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Stderr).To(ContainSubstring("nothing staged"))
	})

	It("fails without a commit message", func() {
		project := newProject("push-no-message")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "file.txt", "content\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "push")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Stderr).To(ContainSubstring("commit message is required"))
	})

	It("makes push a no-op after nipa remove", func() {
		project := newProject("push-remove")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "file.txt", "content\n")
		Expect(runNipa(dir, "add", "file.txt").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "remove", "file.txt").ExitCode).To(Equal(0))

		res := pushRepo(dir, "should not land")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Stderr).To(ContainSubstring("nothing staged"))

		fresh := cloneRepo(parent, url, "fresh")
		Expect(fileExists(fresh, "file.txt")).To(BeFalse())
	})

	It("pushes a deletion of a tracked file", func() {
		project := newProject("push-delete")
		seedRepo(orgSlug, project, map[string][]byte{
			"a.txt": []byte("a\n"),
			"b.txt": []byte("b\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		removePath(dir, "a.txt")
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))

		push := pushRepo(dir, "delete a")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(fileExists(fresh, "a.txt")).To(BeFalse())
		Expect(readText(fresh, "b.txt")).To(Equal("b\n"))
	})

	It("pushes a new binary file without a lock", func() {
		project := newProject("push-binary")
		url := repoURLFor(orgSlug, project)
		payload := randomBytes(256 * 1024)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeBytes(dir, "assets/blob.bin", payload)
		Expect(runNipa(dir, "add", "assets/blob.bin").ExitCode).To(Equal(0))

		push := pushRepo(dir, "add binary")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readBytes(fresh, "assets/blob.bin")).To(Equal(payload))
	})

	It("rejects a stale push and succeeds after update", func() {
		project := newProject("push-stale")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		cloneA := cloneRepo(parent, url, "a")
		cloneB := cloneRepo(parent, url, "b")

		writeText(cloneA, "a.txt", "from a\n")
		Expect(runNipa(cloneA, "add", "a.txt").ExitCode).To(Equal(0))
		pushA := pushRepo(cloneA, "change from a")
		Expect(pushA.ExitCode).To(Equal(0), pushA.Stderr)

		writeText(cloneB, "b.txt", "from b\n")
		Expect(runNipa(cloneB, "add", "b.txt").ExitCode).To(Equal(0))
		pushB := pushRepo(cloneB, "change from b")
		Expect(pushB.ExitCode).To(Equal(2))
		Expect(pushB.Stderr).To(ContainSubstring("has moved"))

		update := runNipa(cloneB, "update")
		Expect(update.ExitCode).To(Equal(0), update.Stderr)
		Expect(readText(cloneB, "a.txt")).To(Equal("from a\n"))

		pushB2 := pushRepo(cloneB, "change from b after update")
		Expect(pushB2.ExitCode).To(Equal(0), pushB2.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "a.txt")).To(Equal("from a\n"))
		Expect(readText(fresh, "b.txt")).To(Equal("from b\n"))
	})

	It("prints the plan with push --dry-run", func() {
		project := newProject("push-dry-run")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "new.txt", "content\n")
		Expect(runNipa(dir, "add", "new.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "push", "--dry-run", "-m", "preview")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(res.Stdout).To(ContainSubstring("A  new.txt"))
		Expect(res.Stdout).To(ContainSubstring("would push 1 file(s)"))
	})
})
