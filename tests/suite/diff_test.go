package suite_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa diff", func() {
	It("shows working-copy changes offline", func() {
		project := newProject("diff-working")
		seedRepo(orgSlug, project, map[string][]byte{
			"tracked.txt": []byte("v1\n"),
			"gone.txt":    []byte("gone\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "tracked.txt", "v2\n")
		removePath(dir, "gone.txt")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "new.txt").ExitCode).To(Equal(0))
		writeText(dir, "untracked.txt", "u\n")

		res := runNipa(dir, "diff")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("diff --nipa a/tracked.txt b/tracked.txt"))
		Expect(res.Stdout).To(ContainSubstring("-v1"))
		Expect(res.Stdout).To(ContainSubstring("+v2"))
		Expect(res.Stdout).To(ContainSubstring("gone.txt"))
		Expect(res.Stdout).To(ContainSubstring("new file mode"))
		Expect(res.Stdout).To(ContainSubstring("+new"))
		Expect(res.Stdout).NotTo(ContainSubstring("untracked.txt"))
	})

	It("shows only staged changes with --staged", func() {
		project := newProject("diff-staged")
		seedRepo(orgSlug, project, map[string][]byte{"tracked.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "tracked.txt", "v2\n")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "new.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "diff", "--staged")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("new.txt"))
		Expect(res.Stdout).NotTo(ContainSubstring("tracked.txt"))
	})

	It("compares a revision against the working copy", func() {
		project := newProject("diff-one-rev")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")

		res := runNipa(dir, "diff", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("b.txt"))
	})

	It("compares two revisions", func() {
		project := newProject("diff-two-rev")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(2))
		first, second := log.Commits[1].ID, log.Commits[0].ID

		res := runNipa(dir, "diff", first, second)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("b.txt"))

		rangeRes := runNipa(dir, "diff", first+".."+second)
		Expect(rangeRes.ExitCode).To(Equal(0), rangeRes.Output())
		Expect(rangeRes.Stdout).To(ContainSubstring("b.txt"))
	})

	It("compares the merge base with three-dot syntax", func() {
		project := newProject("diff-merge-base")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature change").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "main.txt", "main\n")
		Expect(runNipa(dir, "add", "main.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main change").ExitCode).To(Equal(0))

		res := runNipa(dir, "diff", "main...feature")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("feature.txt"))
		Expect(res.Stdout).NotTo(ContainSubstring("main.txt"))
	})

	It("renders --stat, --name-only and --name-status", func() {
		project := newProject("diff-formats")
		seedRepo(orgSlug, project, map[string][]byte{
			"tracked.txt": []byte("v1\n"),
			"gone.txt":    []byte("gone\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "tracked.txt", "v2\n")
		removePath(dir, "gone.txt")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "new.txt", "gone.txt").ExitCode).To(Equal(0))

		stat := runNipa(dir, "diff", "--stat")
		Expect(stat.ExitCode).To(Equal(0), stat.Output())
		Expect(stat.Stdout).To(ContainSubstring("tracked.txt"))
		Expect(stat.Stdout).To(ContainSubstring("3 files changed"))

		names := runNipa(dir, "diff", "--name-only")
		Expect(names.ExitCode).To(Equal(0), names.Output())
		Expect(names.Stdout).To(ContainSubstring("tracked.txt"))
		Expect(names.Stdout).To(ContainSubstring("new.txt"))
		Expect(names.Stdout).To(ContainSubstring("gone.txt"))

		status := runNipa(dir, "diff", "--name-status")
		Expect(status.ExitCode).To(Equal(0), status.Output())
		Expect(status.Stdout).To(ContainSubstring("M\ttracked.txt"))
		Expect(status.Stdout).To(ContainSubstring("A\tnew.txt"))
		Expect(status.Stdout).To(ContainSubstring("D\tgone.txt"))
	})

	It("detects renames", func() {
		project := newProject("diff-rename")
		seedRepo(orgSlug, project, map[string][]byte{"old.txt": []byte("same content\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt"))).To(Succeed())
		Expect(runNipa(dir, "add", "new.txt", "old.txt").ExitCode).To(Equal(0))

		res := runNipa(dir, "diff", "--name-status")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("R100\told.txt\tnew.txt"))
	})

	It("honors -U context and whitespace flags", func() {
		project := newProject("diff-flags")
		seedRepo(orgSlug, project, map[string][]byte{
			"ctx.txt": []byte("line1\nline2\nline3\nline4\nline5\n"),
			"ws.txt":  []byte("a b\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "ctx.txt", "line1\nline2\nCHANGED\nline4\nline5\n")
		writeText(dir, "ws.txt", "a  b\n")

		zero := runNipa(dir, "diff", "-U0")
		Expect(zero.ExitCode).To(Equal(0), zero.Output())
		Expect(zero.Stdout).To(ContainSubstring("@@ -3,1 +3,1 @@"))
		Expect(zero.Stdout).NotTo(ContainSubstring(" line2"))

		ignored := runNipa(dir, "diff", "-w")
		Expect(ignored.ExitCode).To(Equal(0), ignored.Output())
		Expect(ignored.Stdout).NotTo(ContainSubstring("ws.txt"))

		spaceChange := runNipa(dir, "diff", "-b")
		Expect(spaceChange.ExitCode).To(Equal(0), spaceChange.Output())
		Expect(spaceChange.Stdout).NotTo(ContainSubstring("ws.txt"))
	})

	It("returns exit code 1 with --exit-code only when there are changes", func() {
		project := newProject("diff-exit-code")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		clean := runNipa(dir, "diff", "--exit-code")
		Expect(clean.ExitCode).To(Equal(0), clean.Output())

		writeText(dir, "a.txt", "v2\n")
		changed := runNipa(dir, "diff", "--exit-code")
		Expect(changed.ExitCode).To(Equal(1))
		Expect(changed.Stdout).To(ContainSubstring("+v2"))
	})

	It("returns structured hunks as JSON", func() {
		project := newProject("diff-json")
		seedRepo(orgSlug, project, map[string][]byte{"tracked.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "tracked.txt", "v2\n")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "new.txt").ExitCode).To(Equal(0))

		var out diffJSON
		runJSONInto(dir, &out, "diff", "--json")
		Expect(out.Changes).To(HaveLen(2))
		byPath := map[string]int{}
		for i, c := range out.Changes {
			byPath[c.Path] = i
		}
		modified := out.Changes[byPath["tracked.txt"]]
		Expect(modified.Status).To(Equal("M"))
		Expect(modified.Hunks).NotTo(BeEmpty())
		kinds := []string{}
		for _, line := range modified.Hunks[0].Lines {
			kinds = append(kinds, line.Kind)
		}
		Expect(kinds).To(ContainElements("add", "delete"))

		added := out.Changes[byPath["new.txt"]]
		Expect(added.Status).To(Equal("A"))
	})

	It("runs the configured external diff tool", func() {
		project := newProject("diff-external")
		seedRepo(orgSlug, project, map[string][]byte{"tracked.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		script, logPath := externalDiffTool(parent)
		writeText(dir, "tracked.txt", "v2\n")
		writeText(dir, "new.txt", "new\n")
		Expect(runNipa(dir, "add", "new.txt").ExitCode).To(Equal(0))

		res := runNipaEnv(dir, externalDiffEnv(script, logPath), "diff", "--ext-diff")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		data, err := os.ReadFile(logPath)
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		Expect(lines).To(HaveLen(16), "two files, each 7 args plus a count line")
		Expect(lines[0]).To(Equal("7"))
		Expect(lines[8]).To(Equal("7"))
		Expect(strings.Join(lines, "\n")).To(ContainSubstring("tracked.txt"))
		Expect(strings.Join(lines, "\n")).To(ContainSubstring("new.txt"))
		Expect(strings.Join(lines, "\n")).To(ContainSubstring("/dev/null"))
	})

	It("reports binary changes without content", func() {
		project := newProject("diff-binary")
		seedRepo(orgSlug, project, map[string][]byte{"blob.bin": append([]byte{0x00, 0x01}, []byte("binary-one")...)}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeBytes(dir, "blob.bin", append([]byte{0x00, 0x02}, randomBytes(32)...))

		res := runNipa(dir, "diff")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("Binary files"))
		Expect(res.Stdout).To(ContainSubstring("differ"))
	})

	It("limits the diff to paths after --", func() {
		project := newProject("diff-paths")
		seedRepo(orgSlug, project, map[string][]byte{
			"a.txt": []byte("a1\n"),
			"b.txt": []byte("b1\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		writeText(dir, "a.txt", "a2\n")
		writeText(dir, "b.txt", "b2\n")

		res := runNipa(dir, "diff", "--", "a.txt")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("a.txt"))
		Expect(res.Stdout).NotTo(ContainSubstring("b.txt"))
	})

	It("accepts tag names as revisions", func() {
		project := newProject("diff-tag")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "first commit")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v2\n")}, "second commit")

		res := runNipa(dir, "diff", "v1.0.0", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("-v1"))
		Expect(res.Stdout).To(ContainSubstring("+v2"))
	})

	It("fails on an unknown revision", func() {
		project := newProject("diff-bad-rev")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "diff", "no-such-revision")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).NotTo(BeEmpty())
	})

	It("re-reads working files with --no-cache", func() {
		project := newProject("diff-no-cache")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		path := filepath.Join(dir, "a.txt")
		before, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())

		writeText(dir, "a.txt", "v2\n")
		Expect(os.Chtimes(path, before.ModTime(), before.ModTime())).To(Succeed())

		cached := runNipa(dir, "diff")
		Expect(cached.ExitCode).To(Equal(0), cached.Output())
		Expect(cached.Stdout).To(BeEmpty(), "stat cache blind spot hides a same-size, same-mtime edit")

		fresh := runNipa(dir, "diff", "--no-cache")
		Expect(fresh.ExitCode).To(Equal(0), fresh.Output())
		Expect(fresh.Stdout).To(ContainSubstring("+v2"))
	})
})
