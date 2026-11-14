package suite_test

import (
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

var shelfIDPattern = regexp.MustCompile(`\(([0-9a-z]+)\)`)

func shelveID(res commandResult) string {
	GinkgoHelper()
	Expect(res.ExitCode).To(Equal(0), res.Output())
	m := shelfIDPattern.FindStringSubmatch(res.Stdout)
	Expect(m).To(HaveLen(2), res.Output())
	return m[1]
}

var _ = Describe("nipa shelve", func() {
	It("shelves text and binary changes, switches branch and applies them there", func() {
		project := newProject("shelve-switch")
		png := testPNG(8)
		seedRepo(orgSlug, project, map[string][]byte{"notes.txt": []byte("1\n2\n3\n4\n5\n"), "art/hero.png": png}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "notes.txt", "1\n2\n3\n4\nfive\n")
		Expect(runNipa(dir, "add", "notes.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature edit").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		writeText(dir, "notes.txt", "one\n2\n3\n4\n5\n")
		edited := append(append([]byte{}, png...), 0x00, 0x01)
		writeBytes(dir, "art/hero.png", edited)
		writeText(dir, "draft.txt", "draft\n")
		Expect(runNipa(dir, "add", "draft.txt").ExitCode).To(Equal(0))

		id := shelveID(runNipa(dir, "shelve", "-m", "wip"))
		assertCleanStatus(dir)
		Expect(fileExists(dir, "draft.txt")).To(BeFalse())
		Expect(readBytes(dir, "art/hero.png")).To(Equal(png))

		list := runNipa(dir, "shelve", "list")
		Expect(list.Output()).To(ContainSubstring(id))
		Expect(list.Output()).To(ContainSubstring("3 files"))
		show := runNipa(dir, "shelve", "show", id)
		Expect(show.Output()).To(ContainSubstring("A  draft.txt (staged)"))
		Expect(show.Output()).To(ContainSubstring("M  art/hero.png"))

		Expect(runNipa(dir, "switch", "feature").ExitCode).To(Equal(0))
		res := runNipa(dir, "shelve", "apply", id)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`applied shelf "wip"`))
		Expect(readText(dir, "notes.txt")).To(Equal("one\n2\n3\n4\nfive\n"), "the text edit merges onto the other branch")
		Expect(readBytes(dir, "art/hero.png")).To(Equal(edited))
		Expect(readText(dir, "draft.txt")).To(Equal("draft\n"))
		status := runNipa(dir, "status").Output()
		Expect(status).To(ContainSubstring("draft.txt"))
		Expect(runNipa(dir, "shelve", "list").Output()).To(ContainSubstring("no shelves"))
	})

	It("stops on a conflict, refuses to continue with markers, then finishes", func() {
		project := newProject("shelve-conflict")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "mine\n")
		id := shelveID(runNipa(dir, "shelve", "a.txt"))

		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("theirs\n")}, "upstream")
		Expect(runNipa(dir, "update").ExitCode).To(Equal(0))

		res := runNipa(dir, "shelve", "apply", id)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("stopped on conflicts"))
		Expect(readText(dir, "a.txt")).To(ContainSubstring("<<<<<<< ours"))
		Expect(runNipa(dir, "status").Output()).To(ContainSubstring("a.txt"))

		push := pushRepo(dir, "too early")
		Expect(push.ExitCode).NotTo(Equal(0))
		Expect(push.Output()).To(ContainSubstring("stopped on conflicts"))

		res = runNipa(dir, "shelve", "apply", "--continue")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("conflict markers"))

		writeText(dir, "a.txt", "theirs and mine\n")
		res = runNipa(dir, "shelve", "apply", "--continue")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("dropped the shelf"))
		Expect(runNipa(dir, "add", "a.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "resolved").ExitCode).To(Equal(0))
	})

	It("aborts a conflicted apply and keeps the shelf", func() {
		project := newProject("shelve-abort")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "mine\n")
		id := shelveID(runNipa(dir, "shelve"))
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("theirs\n")}, "upstream")
		Expect(runNipa(dir, "update").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "shelve", "apply", id).Output()).To(ContainSubstring("stopped on conflicts"))

		res := runNipa(dir, "shelve", "apply", "--abort")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readText(dir, "a.txt")).To(Equal("theirs\n"))
		assertCleanStatus(dir)
		Expect(runNipa(dir, "shelve", "list").Output()).To(ContainSubstring(id))
		Expect(runNipa(dir, "shelve", "drop", id).ExitCode).To(Equal(0))
	})

	It("refuses to apply over local changes", func() {
		project := newProject("shelve-dirty")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "shelved\n")
		id := shelveID(runNipa(dir, "shelve"))
		writeText(dir, "a.txt", "newer\n")
		res := runNipa(dir, "shelve", "apply", id)
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("would be overwritten"))
		Expect(readText(dir, "a.txt")).To(Equal("newer\n"))
	})

	It("serves shelves and the overwrite check through the daemon", func() {
		project := newProject("shelve-daemon")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n"), "b.txt": []byte("b\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		d := startDaemon()
		d.watch(dir)

		writeText(dir, "a.txt", "local\n")
		writeText(dir, "b.txt", "local b\n")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("upstream\n")}, "upstream")

		plan, err := d.client.SyncPlan(d.ctx, &daemonpb.SyncPlanRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.GetOverwritten()).To(Equal([]string{"a.txt"}), "only the file the update rewrites")

		stream, err := d.client.ShelveCreate(d.ctx, &daemonpb.ShelveCreateRequest{Root: dir, Name: "Before updating", Paths: plan.GetOverwritten()})
		Expect(err).NotTo(HaveOccurred())
		shelf := opResult(collectOpEvents(stream)).GetShelve().GetShelf()
		Expect(shelf.GetEntries()).To(HaveLen(1))
		Expect(readText(dir, "a.txt")).To(Equal("a\n"))
		Expect(readText(dir, "b.txt")).To(Equal("local b\n"))

		list, err := d.client.ShelveList(d.ctx, &daemonpb.ShelveListRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetShelves()).To(HaveLen(1))
		files, err := d.client.ShelveFiles(d.ctx, &daemonpb.ShelveFilesRequest{Root: dir, Id: shelf.GetId()})
		Expect(err).NotTo(HaveOccurred())
		Expect(files.GetFiles()[0].GetAdditions()).To(BeNumerically("==", 1))
		_, blob, failure := collectBlob(mustReadBlob(d, dir, &daemonpb.BlobRef{Source: "shelf", Revision: shelf.GetId(), Path: "a.txt"}))
		Expect(failure).To(BeNil())
		Expect(string(blob)).To(Equal("local\n"))

		update, err := d.client.Update(d.ctx, &daemonpb.UpdateRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(opResult(collectOpEvents(update))).NotTo(BeNil())
		Expect(readText(dir, "a.txt")).To(Equal("upstream\n"))

		apply, err := d.client.ShelveApply(d.ctx, &daemonpb.ShelveApplyRequest{Root: dir, Id: shelf.GetId()})
		Expect(err).NotTo(HaveOccurred())
		result := opResult(collectOpEvents(apply)).GetShelveApply()
		Expect(result.GetConflicts()).To(Equal([]string{"a.txt"}))
		conflicts, err := d.client.Conflicts(d.ctx, &daemonpb.ConflictsRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicts.GetOp()).To(Equal("shelf"))

		_, err = d.client.ResolveConflict(d.ctx, &daemonpb.ResolveConflictRequest{Root: dir, Path: "a.txt", Choice: "theirs"})
		Expect(err).NotTo(HaveOccurred())
		Expect(readText(dir, "a.txt")).To(Equal("local\n"))
		Expect(d.status(dir).GetPending()).To(BeEmpty())
		list, err = d.client.ShelveList(d.ctx, &daemonpb.ShelveListRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetShelves()).To(BeEmpty())
	})
})

func mustReadBlob(d *daemonProc, root string, ref *daemonpb.BlobRef) daemonpb.NipaDaemon_ReadBlobClient {
	GinkgoHelper()
	stream, err := d.client.ReadBlob(d.ctx, &daemonpb.ReadBlobRequest{Root: root, Blob: ref})
	Expect(err).NotTo(HaveOccurred())
	return stream
}
