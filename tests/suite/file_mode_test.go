package suite_test

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func chmodPath(dir, path string, perm os.FileMode) {
	GinkgoHelper()
	Expect(os.Chmod(filepath.Join(dir, filepath.FromSlash(path)), perm)).To(Succeed())
}

func pathPerm(dir, path string) os.FileMode {
	GinkgoHelper()
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path)))
	Expect(err).NotTo(HaveOccurred())
	return info.Mode().Perm()
}

var _ = Describe("nipa push file modes", func() {
	BeforeEach(func() {
		if runtime.GOOS == "windows" {
			Skip("Unix permission bits are not tracked on Windows")
		}
	})

	It("pushes non-canonical executable and read-only permissions with a clean status", func() {
		project := newProject("push-file-modes")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		perms := map[string]os.FileMode{
			"bin/group.sh": 0o775,
			"bin/owner.sh": 0o700,
			"bin/ro-x.sh":  0o555,
			"docs/ro.txt":  0o400,
			"docs/rw.txt":  0o666,
		}
		args := []string{"add"}
		for path, perm := range perms {
			writeText(dir, path, "content of "+path+"\n")
			chmodPath(dir, path, perm)
			args = append(args, path)
		}
		res := runNipa(dir, args...)
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		push := pushRepo(dir, "add files with assorted modes")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)
		assertCleanStatus(dir)

		again := pushRepo(dir, "nothing left")
		Expect(again.ExitCode).To(Equal(1))
		Expect(again.Stderr).To(ContainSubstring("nothing staged"))

		fresh := cloneRepo(parent, url, "fresh")
		for _, path := range []string{"bin/group.sh", "bin/owner.sh", "bin/ro-x.sh"} {
			Expect(pathPerm(fresh, path)&0o111).NotTo(BeZero(), "%s must be executable in a fresh clone", path)
		}
		Expect(pathPerm(fresh, "docs/ro.txt")&0o333).To(BeZero(), "docs/ro.txt must be read-only in a fresh clone")
		Expect(pathPerm(fresh, "docs/rw.txt") & 0o111).To(BeZero())
		Expect(pathPerm(fresh, "docs/rw.txt") & 0o200).NotTo(BeZero())
		assertCleanStatus(fresh)
	})

	// Pending until the server stores mode-only changes: tree hashes ignore
	// file modes and tree contents are shared by hash, so a chmod-only push
	// reads back the previous mode (see internal/e2e TestEndToEnd_FileModeOnlyChange).
	PIt("pushes a mode-only change (0644 -> 0775) with a clean status", func() {
		project := newProject("push-mode-only")
		url := repoURLFor(orgSlug, project)
		seedRepo(orgSlug, project, map[string][]byte{"run.sh": []byte("#!/bin/sh\n")}, "seed")

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		chmodPath(dir, "run.sh", 0o775)
		res := runNipa(dir, "add", "run.sh")
		Expect(res.ExitCode).To(Equal(0), res.Stderr)
		push := pushRepo(dir, "make run.sh executable")
		Expect(push.ExitCode).To(Equal(0), push.Stderr)
		assertCleanStatus(dir)

		fresh := cloneRepo(parent, url, "fresh")
		Expect(pathPerm(fresh, "run.sh") & 0o111).NotTo(BeZero())
		assertCleanStatus(fresh)
	})
})
