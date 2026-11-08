package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa restore and nipa diff without cached objects", func() {
	It("recaptures or downloads the snapshot content", func() {
		project := newProject("pristine-fallback")
		seedRepo(orgSlug, project, map[string][]byte{
			"edited.txt": []byte("v1\n"),
			"same.txt":   []byte("unchanged\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		writeText(dir, "edited.txt", "local edit\n")
		Expect(os.RemoveAll(filepath.Join(dir, ".nipa", "objects"))).To(Succeed())

		res := runNipa(dir, "diff")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(res.Stdout).To(ContainSubstring("-v1"))
		Expect(res.Stdout).To(ContainSubstring("+local edit"))

		res = runNipa(dir, "restore", "edited.txt", "same.txt")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		Expect(readText(dir, "edited.txt")).To(Equal("v1\n"))
		Expect(readText(dir, "same.txt")).To(Equal("unchanged\n"))
		assertCleanStatus(dir)
	})
})
