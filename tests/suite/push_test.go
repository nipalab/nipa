package suite_test

import (
	"os"
	"path/filepath"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa push", func() {
	It("leaves the pusher's tree equal to the server's without a refetch, across adds, edits and removals", func() {
		project := newProject("push-local-apply")
		url := repoURLFor(orgSlug, project)
		seedRepo(orgSlug, project, map[string][]byte{
			"root.txt":            []byte("root\n"),
			"src/a.txt":           []byte("a\n"),
			"src/b.txt":           []byte("b\n"),
			"solo/chain/only.txt": []byte("only\n"),
		}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		other := cloneRepo(parent, url, "other")

		settled := func(step string) {
			assertCleanStatus(dir)
			res := runNipa(dir, "update")
			Expect(res.ExitCode).To(Equal(0), step+": "+res.Output())
			Expect(res.Output()).To(ContainSubstring("Already up to date."), step)
			res = runNipa(other, "update")
			Expect(res.ExitCode).To(Equal(0), step+": "+res.Output())
			assertCleanStatus(other)
		}

		writeText(dir, "src/a.txt", "a v2\n")
		writeText(dir, "src/gen/v1/types.txt", "types\n")
		Expect(runNipa(dir, "add", "src/a.txt", "src/gen/v1/types.txt").ExitCode).To(Equal(0))
		push := pushRepo(dir, "edit and add nested")
		Expect(push.ExitCode).To(Equal(0), push.Output())
		settled("edit and add nested")
		Expect(readText(other, "src/a.txt")).To(Equal("a v2\n"))
		Expect(readText(other, "src/gen/v1/types.txt")).To(Equal("types\n"))

		removePath(dir, "solo/chain/only.txt")
		removePath(dir, "src/b.txt")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "solo/chain/only.txt", "src/b.txt", "new.txt").ExitCode).To(Equal(0))
		push = pushRepo(dir, "remove a directory chain, add at the root")
		Expect(push.ExitCode).To(Equal(0), push.Output())
		settled("remove and add")
		Expect(fileExists(other, "solo/chain/only.txt")).To(BeFalse())
		Expect(fileExists(other, "src/b.txt")).To(BeFalse())
		Expect(readText(other, "new.txt")).To(Equal("new\n"))

		// The second client pushes on top, and the first one follows.
		writeText(other, "root.txt", "root from other\n")
		Expect(runNipa(other, "add", "root.txt").ExitCode).To(Equal(0))
		push = pushRepo(other, "other edits root")
		Expect(push.ExitCode).To(Equal(0), push.Output())
		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).NotTo(ContainSubstring("Already up to date."))
		Expect(readText(dir, "root.txt")).To(Equal("root from other\n"))

		// And pushes again over the tree the update saved.
		writeText(dir, "src/a.txt", "a v3\n")
		Expect(runNipa(dir, "add", "src/a.txt").ExitCode).To(Equal(0))
		push = pushRepo(dir, "edit after update")
		Expect(push.ExitCode).To(Equal(0), push.Output())
		settled("edit after update")

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "src/a.txt")).To(Equal("a v3\n"))
		Expect(readText(fresh, "root.txt")).To(Equal("root from other\n"))
		Expect(fileExists(fresh, "solo/chain/only.txt")).To(BeFalse())
	})

	It("keeps a plugin's read-only guard out of status and pushes", func() {
		project := newProject("push-readonly-guard")
		url := repoURLFor(orgSlug, project)
		seedRepo(orgSlug, project, map[string][]byte{"level.tscn": []byte("v1\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		file := filepath.Join(dir, "level.tscn")
		Expect(os.Chmod(file, 0o444)).To(Succeed())
		assertCleanStatus(dir)

		Expect(os.Chmod(file, 0o644)).To(Succeed())
		writeText(dir, "level.tscn", "v2\n")
		Expect(os.Chmod(file, 0o444)).To(Succeed())
		res := runNipa(dir, "add", "level.tscn")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		push := pushRepo(dir, "edit while guarded")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)
		assertCleanStatus(dir)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "level.tscn")).To(Equal("v2\n"))
		info, err := os.Stat(filepath.Join(fresh, "level.tscn"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o644)), "the guard must not be recorded on the server")
	})

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

	It("pushes an empty file", func() {
		project := newProject("push-empty")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "Content/Localization/EngineOverrides/EngineOverrides_Conflicts.txt", "")
		res := runNipa(dir, "add", "Content/Localization/EngineOverrides/EngineOverrides_Conflicts.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)

		push := pushRepo(dir, "add empty file")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(fileExists(fresh, "Content/Localization/EngineOverrides/EngineOverrides_Conflicts.txt")).To(BeTrue())
		Expect(readText(fresh, "Content/Localization/EngineOverrides/EngineOverrides_Conflicts.txt")).To(BeEmpty())
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
