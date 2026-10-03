package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa clone", func() {
	It("clones the default branch and materializes the tree", func() {
		project := newProject("clone-basic")
		seedRepo(orgSlug, project, map[string][]byte{
			"README.md":   []byte("hello nipa\n"),
			"src/main.go": []byte("package main\n"),
			"docs/a.txt":  []byte("docs\n"),
		}, "seed tree")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(readText(dir, "README.md")).To(Equal("hello nipa\n"))
		Expect(readText(dir, "src/main.go")).To(Equal("package main\n"))
		Expect(readText(dir, "docs/a.txt")).To(Equal("docs\n"))

		cfg := loadRepoConfig(dir)
		Expect(cfg.URL).To(Equal(repoURLFor(orgSlug, project)))
		Expect(cfg.Branch).To(Equal("main"))
		Expect(cfg.Sparse).To(BeEmpty())
		Expect(cfg.Head).To(BeNil())

		assertCleanStatus(dir)
	})

	It("clones into an existing empty directory", func() {
		project := newProject("clone-empty-dir")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("content\n")}, "seed")

		parent := workspace()
		Expect(os.MkdirAll(filepath.Join(parent, "work"), 0o755)).To(Succeed())

		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(readText(dir, "file.txt")).To(Equal("content\n"))
	})

	It("refuses to clone into a non-empty directory", func() {
		project := newProject("clone-nonempty")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("content\n")}, "seed")

		parent := workspace()
		writeText(parent, "work/keep.txt", "keep me")

		res := runNipa(parent, "clone", repoURLFor(orgSlug, project), "work")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Stderr).To(ContainSubstring("not empty"))
		Expect(readText(parent, "work/keep.txt")).To(Equal("keep me"))
		Expect(fileExists(parent, "work/.nipa/config")).To(BeFalse())
	})

	It("fails to clone an unknown project", func() {
		parent := workspace()
		res := runNipa(parent, "clone", "http://"+cli.host+"/"+orgSlug+"/no-such-project", "work")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Stderr).NotTo(BeEmpty())
		Expect(fileExists(parent, "work/.nipa/config")).To(BeFalse())
	})

	It("fails to clone an unknown branch", func() {
		project := newProject("clone-nobranch")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("content\n")}, "seed")

		parent := workspace()
		res := runNipa(parent, "clone", repoURLFor(orgSlug, project), "work", "-b", "nope")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Stderr).NotTo(BeEmpty())
	})

	It("clones only the requested subpath", func() {
		project := newProject("clone-subpath")
		seedRepo(orgSlug, project, map[string][]byte{
			"docs/a.txt": []byte("docs\n"),
			"src/b.txt":  []byte("src\n"),
		}, "seed tree")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project)+"/docs", "work")

		Expect(readText(dir, "docs/a.txt")).To(Equal("docs\n"))
		Expect(fileExists(dir, "src/b.txt")).To(BeFalse())

		cfg := loadRepoConfig(dir)
		Expect(cfg.Sparse).To(Equal([]string{"docs"}))
	})

	It("clones with sparse prefixes", func() {
		project := newProject("clone-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			"src/main.go":  []byte("package main\n"),
			"docs/a.txt":   []byte("docs\n"),
			"assets/l.bin": []byte("binary-ish\n"),
		}, "seed tree")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work", "--sparse", "src,docs")

		Expect(readText(dir, "src/main.go")).To(Equal("package main\n"))
		Expect(readText(dir, "docs/a.txt")).To(Equal("docs\n"))
		Expect(fileExists(dir, "assets/l.bin")).To(BeFalse())

		cfg := loadRepoConfig(dir)
		Expect(cfg.Sparse).To(Equal([]string{"src", "docs"}))
	})

	It("round-trips binary files", func() {
		project := newProject("clone-binary")
		payload := randomBytes(512 * 1024)
		seedRepo(orgSlug, project, map[string][]byte{"assets/blob.bin": payload}, "seed binary")

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(readBytes(dir, "assets/blob.bin")).To(Equal(payload))
	})
})
