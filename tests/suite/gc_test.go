package suite_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func cachedObjects(dir string) []string {
	GinkgoHelper()
	var out []string
	root := filepath.Join(dir, ".nipa", "objects")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(d.Name(), ".tmp-") {
			out = append(out, path)
		}
		return nil
	})
	Expect(err).NotTo(HaveOccurred())
	return out
}

var _ = Describe("nipa gc", func() {
	It("drops replaced versions on update and orphans on gc", func() {
		project := newProject("gc-basic")
		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v1\n")}, "seed")

		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(cachedObjects(dir)).To(HaveLen(1))

		seedRepo(orgSlug, project, map[string][]byte{"file.txt": []byte("v2 with more bytes\n")}, "remote update")
		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(cachedObjects(dir)).To(HaveLen(1), "update must prune the replaced version")

		orphan := filepath.Join(dir, ".nipa", "objects", "ff", strings.Repeat("e", 62))
		Expect(os.MkdirAll(filepath.Dir(orphan), 0o755)).To(Succeed())
		Expect(os.WriteFile(orphan, []byte("orphan"), 0o644)).To(Succeed())

		res = runNipa(dir, "gc")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(res.Stdout).To(ContainSubstring("removed 1 object(s), freed 6 B"))
		Expect(fileExists(dir, ".nipa/objects/ff/"+strings.Repeat("e", 62))).To(BeFalse())
		Expect(cachedObjects(dir)).To(HaveLen(1))

		res = runNipa(dir, "gc")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(res.Stdout).To(ContainSubstring("removed 0 object(s)"))
		Expect(readText(dir, "file.txt")).To(Equal("v2 with more bytes\n"))
		assertCleanStatus(dir)
	})

	It("fails outside a working copy", func() {
		res := runNipa(workspace(), "gc")
		Expect(res.ExitCode).NotTo(Equal(0))
	})
})
