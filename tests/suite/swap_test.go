package suite_test

import (
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// swapTree lists the files of a working copy (.nipa excluded) with their
// content, and every folder with an empty value under its path plus "/".
func swapTree(dir string) map[string]string {
	GinkgoHelper()
	out := map[string]string{}
	Expect(filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel == ".":
		case d.IsDir() && rel == ".nipa":
			return filepath.SkipDir
		case d.IsDir():
			out[rel+"/"] = ""
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = string(data)
		}
		return nil
	})).To(Succeed())
	return out
}

// A path that is a file in one commit and a folder in the other, both ways
// and several levels deep, in one commit.
var (
	swapBefore = map[string]string{
		"keep.txt": "keep\n", "a/b": "b file\n", "a/x/y": "y\n", "a/x/z/w.txt": "w\n",
		"a/": "", "a/x/": "", "a/x/z/": "",
	}
	swapAfter = map[string]string{
		"keep.txt": "keep\n", "a/b/c/d.txt": "d\n", "a/x": "x file\n",
		"a/": "", "a/b/": "", "a/b/c/": "",
	}
)

// seedSwap lands swapAfter on top of swapBefore as one commit.
func seedSwap(project string) {
	GinkgoHelper()
	seedRun(orgSlug, project, "swap files and folders", func(dir string) []string {
		removePath(dir, "a/b")
		removePath(dir, "a/x/y")
		removePath(dir, "a/x/z/w.txt")
		Expect(os.RemoveAll(filepath.Join(dir, "a", "x"))).To(Succeed())
		writeText(dir, "a/b/c/d.txt", "d\n")
		writeText(dir, "a/x", "x file\n")
		return []string{"a/b", "a/b/c/d.txt", "a/x", "a/x/y", "a/x/z/w.txt"}
	})
}

func seedSwapBase(project string) {
	GinkgoHelper()
	seedRepo(orgSlug, project, map[string][]byte{
		"keep.txt":    []byte("keep\n"),
		"a/b":         []byte("b file\n"),
		"a/x/y":       []byte("y\n"),
		"a/x/z/w.txt": []byte("w\n"),
	}, "seed")
}

var _ = Describe("file/folder swaps", func() {
	It("updates and switches across a file that becomes a folder and a folder that becomes a file", func() {
		project := newProject("swap-update")
		seedSwapBase(project)
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(swapTree(dir)).To(Equal(swapBefore))
		// Name the old commit by a branch and a tag.
		Expect(runNipa(dir, "tag", "-c", "before").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "branch", "-c", "old").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		seedSwap(project)
		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapAfter))
		assertCleanStatus(dir)

		res = runNipa(dir, "switch", "old")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapBefore))

		res = runNipa(dir, "switch", "main", "--force")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapAfter))

		res = runNipa(dir, "switch", "--tag", "before")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapBefore))

		res = runNipa(dir, "switch", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapAfter))
		assertCleanStatus(dir)

		// The forced full update of a clone already there changes nothing.
		res = runNipa(dir, "update", "--force")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapAfter))
	})

	It("refuses, even forced, a swap that would delete untracked or edited files, and goes through once they are moved", func() {
		project := newProject("swap-refuse")
		seedSwapBase(project)
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a/x/z/mine.txt", "mine\n")
		writeText(dir, "a/b", "edited b\n")
		seedSwap(project)

		before := swapTree(dir)
		for _, args := range [][]string{{"update"}, {"update", "--force"}} {
			res := runNipa(dir, args...)
			Expect(res.ExitCode).NotTo(Equal(0), res.Output())
			Expect(res.Output()).To(ContainSubstring("a/x/z/mine.txt"))
			Expect(res.Output()).To(ContainSubstring("a/b (locally changed)"))
			Expect(res.Output()).To(ContainSubstring("nothing was changed"))
			Expect(swapTree(dir)).To(Equal(before))
		}

		removePath(dir, "a/x/z/mine.txt")
		writeText(dir, "a/b", "b file\n")
		res := runNipa(dir, "update")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapAfter))
		assertCleanStatus(dir)
	})

	It("pushes a swap made in the working copy", func() {
		project := newProject("swap-push")
		seedSwapBase(project)
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		removePath(dir, "a/b")
		writeText(dir, "a/b/c/d.txt", "d\n")
		Expect(os.RemoveAll(filepath.Join(dir, "a", "x"))).To(Succeed())
		writeText(dir, "a/x", "x file\n")
		res := runNipa(dir, "add", ".")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		res = pushRepo(dir, "swap locally")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		assertCleanStatus(dir)

		fresh := cloneRepo(workspace(), repoURLFor(orgSlug, project), "fresh")
		Expect(swapTree(fresh)).To(Equal(swapAfter))
		res = runNipa(dir, "update", "--force")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(swapTree(dir)).To(Equal(swapAfter))
	})
})
