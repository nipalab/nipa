package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type blameJSON struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
	Lines    []struct {
		Line        int    `json:"line"`
		Text        string `json:"text"`
		CommitID    string `json:"commit_id"`
		Uncommitted bool   `json:"uncommitted"`
	} `json:"lines"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
	Truncated bool `json:"truncated"`
}

func blameOwners(out blameJSON) []string {
	owners := make([]string, len(out.Lines))
	for i, l := range out.Lines {
		owners[i] = l.CommitID
		if l.Uncommitted {
			owners[i] = "new"
		}
	}
	return owners
}

var _ = Describe("nipa blame", func() {
	It("attributes lines to the commits that changed them and marks unpushed lines", func() {
		project := newProject("blame-lines")
		seedRepo(orgSlug, project, map[string][]byte{"src/a.txt": []byte("one\ntwo\n")}, "add a")
		seedRepo(orgSlug, project, map[string][]byte{"other.txt": []byte("x\n")}, "unrelated")
		seedRepo(orgSlug, project, map[string][]byte{"src/a.txt": []byte("one\nTWO\nthree\n")}, "change a")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(3))
		newer, older := log.Commits[0].ID, log.Commits[2].ID

		writeBytes(dir, "src/a.txt", []byte("one\nTWO\nthree\nfour\n"))
		var out blameJSON
		runJSONInto(dir, &out, "blame", "src/a.txt", "--json")
		Expect(blameOwners(out)).To(Equal([]string{older, newer, newer, "new"}))
		Expect(out.Lines[3].Text).To(Equal("four"))
		Expect(out.Revision).To(BeEmpty())
		Expect(out.Truncated).To(BeFalse())
		Expect(out.Commits).To(HaveLen(2))
		Expect(out.Commits[0].ID).To(Equal(newer))

		var rev blameJSON
		runJSONInto(dir, &rev, "blame", "-r", older, "src/a.txt", "--json")
		Expect(blameOwners(rev)).To(Equal([]string{older, older}))
		Expect(rev.Revision).To(Equal(older))

		res := runNipa(dir, "blame", "src/a.txt")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring(newer))
		Expect(res.Stdout).To(ContainSubstring("not pushed"))
	})

	It("refuses binary and missing files", func() {
		project := newProject("blame-refuse")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "add a")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeBytes(dir, "img.bin", []byte{0, 1, 2, 3})

		res := runNipa(dir, "blame", "img.bin")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("not a text file"))

		res = runNipa(dir, "blame", "missing.txt")
		Expect(res.ExitCode).NotTo(Equal(0))
	})
})
