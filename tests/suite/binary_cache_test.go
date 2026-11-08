package suite_test

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func goOffline(dir string) func() {
	GinkgoHelper()
	path := filepath.Join(dir, ".nipa", "config")
	original, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	cfg := loadRepoConfig(dir)
	u, err := url.Parse(cfg.URL)
	Expect(err).NotTo(HaveOccurred())
	offline := strings.Replace(string(original), cfg.URL, strings.Replace(cfg.URL, u.Host, "127.0.0.1:1", 1), 1)
	Expect(os.WriteFile(path, []byte(offline), 0o644)).To(Succeed())
	return func() { Expect(os.WriteFile(path, original, 0o644)).To(Succeed()) }
}

var _ = Describe("nipa clone binary cache", func() {
	It("keeps text but not raw binary content in the object cache", func() {
		project := newProject("cache-binary-clone")
		seedRepo(orgSlug, project, map[string][]byte{
			"notes.txt":       []byte("text stays cached\n"),
			"assets/hero.bin": append([]byte{0x00}, randomBytes(64<<10)...),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(cachedObjects(dir)).To(HaveLen(1), "only the compressed text chunk is cached")
	})
})

var _ = Describe("nipa lock pins", func() {
	It("keeps the old version of a locked binary for offline diff and restore", func() {
		project := newProject("cache-binary-lock")
		original := append([]byte{0x00}, randomBytes(32<<10)...)
		seedRepo(orgSlug, project, map[string][]byte{"assets/hero.bin": original}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(cachedObjects(dir)).To(BeEmpty())

		res := runNipa(dir, "lock", "assets/hero.bin")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stderr).NotTo(ContainSubstring("warning"))
		Expect(cachedObjects(dir)).NotTo(BeEmpty(), "locking captures the old version")

		writeBytes(dir, "assets/hero.bin", append([]byte{0x00}, randomBytes(32<<10)...))
		restore := goOffline(dir)
		defer restore()

		script, logPath := externalDiffTool(parent)
		res = runNipaEnv(dir, externalDiffEnv(script, logPath), "diff", "--ext-diff")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stderr).NotTo(ContainSubstring("not available"))
		data, err := os.ReadFile(logPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("assets/hero.bin"))

		res = runNipa(dir, "restore", "assets/hero.bin")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readBytes(dir, "assets/hero.bin")).To(Equal(original))

		restore()
		res = runNipa(dir, "unlock", "assets/hero.bin")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(cachedObjects(dir)).To(BeEmpty(), "unlocking drops the kept version")
	})

	It("downloads an unlocked binary again on restore", func() {
		project := newProject("cache-binary-restore")
		original := append([]byte{0x00}, randomBytes(32<<10)...)
		seedRepo(orgSlug, project, map[string][]byte{"assets/hero.bin": original}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeBytes(dir, "assets/hero.bin", append([]byte{0x00}, randomBytes(1024)...))
		res := runNipa(dir, "restore", "assets/hero.bin")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readBytes(dir, "assets/hero.bin")).To(Equal(original))
		assertCleanStatus(dir)
	})
})
