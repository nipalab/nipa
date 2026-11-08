package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa status", func() {
	It("reports staged, deleted, modified, untracked and missing paths as JSON", func() {
		project := newProject("status-json")
		seedRepo(orgSlug, project, map[string][]byte{
			"edited.txt":   []byte("edited v1\n"),
			"restaged.txt": []byte("restaged v1\n"),
			"dropped.txt":  []byte("dropped\n"),
			"vanished.txt": []byte("vanished\n"),
			"clean.txt":    []byte("clean\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		var st statusJSON
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Branch).To(Equal("main"))
		Expect(st.Head).To(BeNil())
		Expect(st.Staged).To(BeEmpty())
		Expect(st.Deleted).To(BeEmpty())
		Expect(st.Modified).To(BeEmpty())
		Expect(st.Untracked).To(BeEmpty())
		Expect(st.Missing).To(BeEmpty())
		Expect(st.Conflicts).To(BeEmpty())

		writeText(dir, "added.txt", "added\n")
		writeText(dir, "restaged.txt", "restaged v2 longer\n")
		Expect(runNipa(dir, "add", "added.txt", "restaged.txt").ExitCode).To(Equal(0))
		removePath(dir, "dropped.txt")
		Expect(runNipa(dir, "add", "dropped.txt").ExitCode).To(Equal(0))
		writeText(dir, "edited.txt", "edited v2 longer\n")
		writeText(dir, "stray/untracked.txt", "stray\n")
		removePath(dir, "vanished.txt")

		st = statusJSON{}
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Branch).To(Equal("main"))
		Expect(st.Staged).To(ConsistOf("added.txt", "restaged.txt"))
		Expect(st.Deleted).To(ConsistOf("dropped.txt"))
		Expect(st.Modified).To(ConsistOf("edited.txt"))
		Expect(st.Untracked).To(ConsistOf("stray/untracked.txt"))
		Expect(st.Missing).To(ConsistOf("vanished.txt"))
		Expect(st.Conflicts).To(BeEmpty())

		human := runNipa(dir, "status")
		Expect(human.ExitCode).To(Equal(0), human.Output())
		Expect(human.Output()).To(ContainSubstring("A  added.txt"))
		Expect(human.Output()).To(ContainSubstring("D  dropped.txt"))
		Expect(human.Output()).To(ContainSubstring("M  edited.txt"))
		Expect(human.Output()).To(ContainSubstring("?  stray/untracked.txt"))
		Expect(human.Output()).To(ContainSubstring("!  vanished.txt"))
	})

	It("detects a same-size, same-mtime edit only with --no-cache", func() {
		project := newProject("status-no-cache")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n"), "b.txt": []byte("b\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		var st statusJSON
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Modified).To(BeEmpty())

		path := filepath.Join(dir, "a.txt")
		before, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		writeText(dir, "a.txt", "v2\n")
		Expect(os.Chtimes(path, before.ModTime(), before.ModTime())).To(Succeed())
		after, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Size()).To(Equal(before.Size()))
		Expect(after.ModTime()).To(Equal(before.ModTime()))

		st = statusJSON{}
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Modified).To(BeEmpty(), "a fresh fingerprint is trusted without reading the file")

		st = statusJSON{}
		runJSONInto(dir, &st, "status", "--json", "--no-cache")
		Expect(st.Modified).To(ConsistOf("a.txt"))

		st = statusJSON{}
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Modified).To(ConsistOf("a.txt"), "--no-cache refreshes the stat cache")
	})

	It("stages a directory with add <dir>, skipping unchanged tracked files", func() {
		project := newProject("status-add-dir")
		seedRepo(orgSlug, project, map[string][]byte{
			"assets/kept.txt":    []byte("kept\n"),
			"assets/changed.txt": []byte("changed v1\n"),
			"outside.txt":        []byte("outside\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeText(dir, "assets/new.txt", "new\n")
		writeText(dir, "assets/sub/deep.txt", "deep\n")
		writeText(dir, "assets/changed.txt", "changed v2 longer\n")
		writeText(dir, "top.txt", "top\n")
		writeText(dir, "outside.txt", "outside v2 longer\n")

		res := runNipa(dir, "add", "assets")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		var st statusJSON
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Staged).To(ConsistOf("assets/new.txt", "assets/sub/deep.txt", "assets/changed.txt"))
		Expect(st.Untracked).To(ConsistOf("top.txt"))
		Expect(st.Modified).To(ConsistOf("outside.txt"))

		Expect(pushRepo(dir, "add assets dir").ExitCode).To(Equal(0))
		fresh := cloneRepo(workspace(), repoURLFor(orgSlug, project), "fresh")
		Expect(readText(fresh, "assets/sub/deep.txt")).To(Equal("deep\n"))
		Expect(readText(fresh, "assets/changed.txt")).To(Equal("changed v2 longer\n"))
		Expect(fileExists(fresh, "top.txt")).To(BeFalse())
		Expect(readText(fresh, "outside.txt")).To(Equal("outside\n"))
	})

	It("stages the whole working copy with add .", func() {
		project := newProject("status-add-dot")
		seedRepo(orgSlug, project, map[string][]byte{
			"kept.txt": []byte("kept\n"),
			"gone.txt": []byte("gone\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeText(dir, "root.txt", "root\n")
		writeText(dir, "nested/leaf.txt", "leaf\n")
		removePath(dir, "gone.txt")

		res := runNipa(dir, "add", ".")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		var st statusJSON
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Staged).To(ConsistOf("root.txt", "nested/leaf.txt"))
		Expect(st.Deleted).To(ConsistOf("gone.txt"))
		Expect(st.Untracked).To(BeEmpty())
		Expect(st.Missing).To(BeEmpty())
	})

	It("unstages everything with remove --all and keeps the files on disk", func() {
		project := newProject("status-remove-all")
		seedRepo(orgSlug, project, map[string][]byte{
			"tracked.txt": []byte("tracked v1\n"),
			"gone.txt":    []byte("gone\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeText(dir, "new.txt", "new\n")
		writeText(dir, "dir/inner.txt", "inner\n")
		writeText(dir, "tracked.txt", "tracked v2 longer\n")
		removePath(dir, "gone.txt")
		Expect(runNipa(dir, "add", "new.txt", "dir", "tracked.txt", "gone.txt").ExitCode).To(Equal(0))

		var st statusJSON
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Staged).To(ConsistOf("new.txt", "dir/inner.txt", "tracked.txt"))
		Expect(st.Deleted).To(ConsistOf("gone.txt"))

		noArgs := runNipa(dir, "remove")
		Expect(noArgs.ExitCode).To(Equal(1))
		Expect(noArgs.Output()).To(ContainSubstring("use -a to unmark everything"))

		Expect(runNipa(dir, "remove", "dir").ExitCode).To(Equal(0))
		st = statusJSON{}
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Staged).To(ConsistOf("new.txt", "tracked.txt"))
		Expect(st.Untracked).To(ConsistOf("dir/inner.txt"))

		res := runNipa(dir, "remove", "--all")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		st = statusJSON{}
		runJSONInto(dir, &st, "status", "--json")
		Expect(st.Staged).To(BeEmpty())
		Expect(st.Deleted).To(BeEmpty())
		Expect(st.Untracked).To(ConsistOf("new.txt", "dir/inner.txt"))
		Expect(st.Modified).To(ConsistOf("tracked.txt"))
		Expect(st.Missing).To(ConsistOf("gone.txt"))
		Expect(readText(dir, "new.txt")).To(Equal("new\n"))
		Expect(readText(dir, "tracked.txt")).To(Equal("tracked v2 longer\n"))

		push := pushRepo(dir, "nothing staged")
		Expect(push.ExitCode).To(Equal(1))
		Expect(push.Stderr).To(ContainSubstring("nothing staged"))
	})
})
