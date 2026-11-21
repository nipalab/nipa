package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa ignore rules", func() {
	It("hides ignored untracked files from status and skips them on add", func() {
		project := newProject("ignore-status")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaignore": []byte("*.log\nbuild/\n"),
			"a.txt":       []byte("a\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeText(dir, "app.log", "log\n")
		writeText(dir, "build/out.o", "o\n")

		res := runNipa(dir, "status")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).NotTo(ContainSubstring("app.log"))
		Expect(res.Output()).NotTo(ContainSubstring("build/out.o"))

		add := runNipa(dir, "add", "")
		Expect(add.ExitCode).To(Equal(0), add.Output())
		res = runNipa(dir, "status")
		Expect(res.Output()).NotTo(ContainSubstring("app.log"))
		Expect(res.Output()).NotTo(ContainSubstring("build/out.o"))
	})

	It("requires -f for an explicitly ignored file and tracks it after push", func() {
		project := newProject("ignore-force")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaignore": []byte("*.log\n"),
			"a.txt":       []byte("a\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "app.log", "log\n")

		res := runNipa(dir, "add", "app.log")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("ignored"))
		Expect(res.Output()).To(ContainSubstring("-f"))

		res = runNipa(dir, "add", "-f", "app.log")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		push := pushRepo(dir, "force add log")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		full := cloneRepo(parent, url, "full")
		Expect(readText(full, "app.log")).To(Equal("log\n"))

		writeText(full, "other.log", "other\n")
		res = runNipa(full, "status")
		Expect(res.Output()).NotTo(ContainSubstring("other.log"), "new files still match the rules")
	})

	It("materializes the root .nipaignore in sparse clones", func() {
		project := newProject("ignore-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaignore": []byte("*.log\n"),
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work", "--sparse", "src")

		Expect(fileExists(dir, ".nipaignore")).To(BeTrue())
		Expect(readText(dir, ".nipaignore")).To(Equal("*.log\n"))

		writeText(dir, "tmp.log", "log\n")
		res := runNipa(dir, "status")
		Expect(res.Output()).NotTo(ContainSubstring("tmp.log"))

		res = runNipa(dir, "add", "tmp.log")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("ignored"))
	})

	Context("nipa ignore import", func() {
		It("creates .nipaignore from the root .gitignore and the rules apply", func() {
			project := newProject("ignore-import")
			seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
			url := repoURLFor(orgSlug, project)
			parent := workspace()
			dir := cloneRepo(parent, url, "work")
			Expect(fileExists(dir, ".nipaignore")).To(BeFalse())

			// A BOM, comments, blank lines and surrounding spaces are dropped.
			writeText(dir, ".gitignore", "\ufeff# build output\n*.log\n\n  build/  \n# trailing comment\n")
			res := runNipa(dir, "ignore", "import")
			Expect(res.ExitCode).To(Equal(0), res.Output())
			Expect(res.Stdout).To(ContainSubstring("added 2 rules to .nipaignore"))
			Expect(res.Stderr).NotTo(ContainSubstring("warning"))
			Expect(readText(dir, ".nipaignore")).To(Equal("# imported from .gitignore\n*.log\nbuild/\n"))

			writeText(dir, "app.log", "log\n")
			writeText(dir, "build/out.o", "o\n")
			status := runNipa(dir, "status")
			Expect(status.ExitCode).To(Equal(0), status.Output())
			Expect(status.Output()).NotTo(ContainSubstring("app.log"))
			Expect(status.Output()).NotTo(ContainSubstring("build/out.o"))
			Expect(status.Output()).To(ContainSubstring(".nipaignore"), "the new rules file is an untracked change until staged")

			add := runNipa(dir, "add", ".nipaignore")
			Expect(add.ExitCode).To(Equal(0), add.Output())
			push := pushRepo(dir, "import ignore rules")
			Expect(push.ExitCode).To(Equal(0), push.Output())

			// The rules travel with the project; .gitignore is never read by
			// nipa itself, so a fresh clone ignores through .nipaignore only.
			other := cloneRepo(parent, url, "other")
			Expect(readText(other, ".nipaignore")).To(Equal("# imported from .gitignore\n*.log\nbuild/\n"))
			writeText(other, "fresh.log", "log\n")
			Expect(runNipa(other, "status").Output()).NotTo(ContainSubstring("fresh.log"))
		})

		It("appends only the missing rules and is idempotent", func() {
			project := newProject("ignore-import-merge")
			seedRepo(orgSlug, project, map[string][]byte{
				".nipaignore": []byte("*.log"),
				"a.txt":       []byte("a\n"),
			}, "seed")
			dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
			Expect(os.Chmod(filepath.Join(dir, ".nipaignore"), 0o600)).To(Succeed())

			writeText(dir, ".gitignore", "*.log\n*.tmp\n*.tmp\ndist/\n")
			res := runNipa(dir, "ignore", "import")
			Expect(res.ExitCode).To(Equal(0), res.Output())
			Expect(res.Stdout).To(ContainSubstring("added 2 rules to .nipaignore"))
			// The existing file had no trailing newline; the import keeps its
			// content, separates the block with a blank line and adds the
			// duplicate rule once.
			Expect(readText(dir, ".nipaignore")).To(Equal("*.log\n\n# imported from .gitignore\n*.tmp\ndist/\n"))
			info, err := os.Stat(filepath.Join(dir, ".nipaignore"))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)), "the rules file keeps its mode")

			again := runNipa(dir, "ignore", "import")
			Expect(again.ExitCode).To(Equal(0), again.Output())
			Expect(again.Stdout).To(ContainSubstring("added 0 rules to .nipaignore"))
			Expect(readText(dir, ".nipaignore")).To(Equal("*.log\n\n# imported from .gitignore\n*.tmp\ndist/\n"))

			status := runNipa(dir, "status")
			Expect(status.Output()).To(ContainSubstring(".nipaignore"), "the merged rules are a pending change")
		})

		It("copies unsupported gitignore syntax with a warning", func() {
			project := newProject("ignore-import-warn")
			seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
			dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

			writeText(dir, ".gitignore", "[Bb]uild/\n\\#literal\n*.bak\n")
			res := runNipa(dir, "ignore", "import")
			Expect(res.ExitCode).To(Equal(0), res.Output())
			Expect(res.Stdout).To(ContainSubstring("added 3 rules to .nipaignore"))
			Expect(res.Stderr).To(ContainSubstring(`warning: "[Bb]uild/" uses gitignore syntax nipa does not support and will not match`))
			Expect(res.Stderr).To(ContainSubstring(`warning: "\\#literal" uses gitignore syntax`))
			Expect(res.Stderr).NotTo(ContainSubstring(`"*.bak"`))
			Expect(readText(dir, ".nipaignore")).To(ContainSubstring("[Bb]uild/\n"))

			// The unsupported rule really does not match, the plain one does.
			writeText(dir, "Build/x.o", "o\n")
			writeText(dir, "old.bak", "b\n")
			status := runNipa(dir, "status")
			Expect(status.Output()).To(ContainSubstring("Build/x.o"))
			Expect(status.Output()).NotTo(ContainSubstring("old.bak"))
		})

		It("runs from a subdirectory against the repository root", func() {
			project := newProject("ignore-import-subdir")
			seedRepo(orgSlug, project, map[string][]byte{"src/main.go": []byte("package main\n")}, "seed")
			dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
			writeText(dir, ".gitignore", "*.out\n")
			writeText(dir, "src/.gitignore", "*.nested\n")

			res := runNipa(filepath.Join(dir, "src"), "ignore", "import")
			Expect(res.ExitCode).To(Equal(0), res.Output())
			Expect(res.Stdout).To(ContainSubstring("added 1 rules to .nipaignore"))
			Expect(readText(dir, ".nipaignore")).To(Equal("# imported from .gitignore\n*.out\n"))
			Expect(fileExists(dir, "src/.nipaignore")).To(BeFalse(), "only the root .gitignore is imported")
		})

		It("fails without a .gitignore, outside a repository and with arguments", func() {
			project := newProject("ignore-import-errors")
			seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
			dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

			res := runNipa(dir, "ignore", "import")
			Expect(res.ExitCode).To(Equal(1), res.Output())
			Expect(res.Output()).To(ContainSubstring("read .gitignore"))
			Expect(fileExists(dir, ".nipaignore")).To(BeFalse(), "a failed import writes nothing")

			res = runNipa(workspace(), "ignore", "import")
			Expect(res.ExitCode).To(Equal(1), res.Output())
			Expect(res.Output()).To(ContainSubstring("not a nipa repository"))

			writeText(dir, ".gitignore", "*.log\n")
			res = runNipa(dir, "ignore", "import", "extra")
			Expect(res.ExitCode).To(Equal(1), res.Output())
			Expect(res.Output()).To(ContainSubstring("unknown command"))
			Expect(fileExists(dir, ".nipaignore")).To(BeFalse())

			res = runNipa(dir, "ignore")
			Expect(res.ExitCode).To(Equal(0), res.Output())
			Expect(res.Output()).To(ContainSubstring("import"))
		})
	})
})
