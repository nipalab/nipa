package suite_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

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

var _ = Describe("nipa shelve selection and edge cases", func() {
	It("takes untracked files only when named or with -u", func() {
		project := newProject("shelve-untracked")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "edited\n")
		writeText(dir, "art/new.txt", "new\n")
		writeText(dir, "loose.txt", "loose\n")

		res := runNipa(dir, "shelve", "art")
		Expect(res.ExitCode).To(Equal(1), res.Output())
		Expect(res.Output()).To(ContainSubstring("art has no changes to shelve"))

		id := shelveID(runNipa(dir, "shelve", "-u", "art"))
		Expect(runNipa(dir, "shelve", "show", id).Output()).To(ContainSubstring("A  art/new.txt (untracked)"))
		Expect(fileExists(dir, "art/new.txt")).To(BeFalse())
		Expect(readText(dir, "a.txt")).To(Equal("edited\n"), "paths outside the selection stay")
		Expect(fileExists(dir, "loose.txt")).To(BeTrue())
	})

	It("shelves an exact untracked path, then everything with -u", func() {
		project := newProject("shelve-untracked-u")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "edited\n")
		writeText(dir, "art/new.txt", "new\n")
		writeText(dir, "loose.txt", "loose\n")

		loose := shelveID(runNipa(dir, "shelve", "--", "loose.txt"))
		Expect(fileExists(dir, "loose.txt")).To(BeFalse())
		Expect(runNipa(dir, "shelve", "show", loose).Output()).To(ContainSubstring("A  loose.txt (untracked)"))

		plain := shelveID(runNipa(dir, "shelve"))
		Expect(runNipa(dir, "shelve", "show", plain).Output()).To(ContainSubstring("M  a.txt"))
		Expect(fileExists(dir, "art/new.txt")).To(BeTrue(), "plain shelve leaves untracked files")

		all := shelveID(runNipa(dir, "shelve", "-u", "-m", "rest"))
		Expect(runNipa(dir, "shelve", "show", all).Output()).To(ContainSubstring("A  art/new.txt (untracked)"))
		Expect(fileExists(dir, "art")).To(BeFalse(), "the emptied folder goes too")

		res := runNipa(dir, "shelve")
		Expect(res.ExitCode).To(Equal(1))
		Expect(res.Output()).To(ContainSubstring("no changes to shelve"))

		Expect(runNipa(dir, "shelve", "apply", loose).ExitCode).To(Equal(0))
		Expect(runNipa(dir, "status").Output()).To(MatchRegexp(`loose\.txt`))
		Expect(readText(dir, "loose.txt")).To(Equal("loose\n"))
	})

	It("reports unknown shelves as not found", func() {
		project := newProject("shelve-missing")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		for _, args := range [][]string{{"shelve", "show", "nope"}, {"shelve", "apply", "nope"}, {"shelve", "drop", "nope"}} {
			res := runNipa(dir, args...)
			Expect(res.ExitCode).To(Equal(127), res.Output())
			Expect(res.Output()).To(ContainSubstring(`shelf "nope" not found`))
		}
		res := runNipa(dir, "shelve", "apply")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("give a shelf id"))
	})

	It("keeps a binary conflict on the synced version until a side is picked", func() {
		project := newProject("shelve-binary-conflict")
		png := testPNG(8)
		seedRepo(orgSlug, project, map[string][]byte{"art/hero.png": png}, "seed")
		url := repoURLFor(orgSlug, project)
		dir := cloneRepo(workspace(), url, "work")
		mine := append(append([]byte{}, png...), 0x01)
		writeBytes(dir, "art/hero.png", mine)
		id := shelveID(runNipa(dir, "shelve"))
		Expect(readBytes(dir, "art/hero.png")).To(Equal(png))

		teammate := cloneRepo(workspace(), url, "teammate")
		Expect(runNipa(teammate, "lock", "art/hero.png").ExitCode).To(Equal(0))
		theirs := append(append([]byte{}, png...), 0x02)
		writeBytes(teammate, "art/hero.png", theirs)
		Expect(runNipa(teammate, "add", "art/hero.png").ExitCode).To(Equal(0))
		Expect(pushRepo(teammate, "teammate art").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "update").ExitCode).To(Equal(0))

		res := runNipa(dir, "shelve", "apply", id, "--keep")
		Expect(res.Output()).To(ContainSubstring("stopped on conflicts"))
		Expect(readBytes(dir, "art/hero.png")).To(Equal(theirs), "the synced version stays on disk")

		drop := runNipa(dir, "shelve", "drop", id)
		Expect(drop.ExitCode).NotTo(Equal(0))
		Expect(drop.Output()).To(ContainSubstring("being applied"))

		res = runNipa(dir, "shelve", "apply", "--continue")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("art/hero.png is still unresolved"))
		Expect(runNipa(dir, "shelve", "list").Output()).To(ContainSubstring(id))

		res = runNipa(dir, "shelve", "resolve", "art/hero.png", "--theirs")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readBytes(dir, "art/hero.png")).To(Equal(mine))
		Expect(runNipa(dir, "status").Output()).NotTo(ContainSubstring("conflict"))
		Expect(runNipa(dir, "shelve", "list").Output()).To(ContainSubstring(id), "--keep keeps it")
		Expect(runNipa(dir, "status").Output()).To(ContainSubstring("art/hero.png"))
	})

	It("shelves while detached at a tag and applies on the branch", func() {
		project := newProject("shelve-tag")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1").ExitCode).To(Equal(0))
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "later")
		Expect(runNipa(dir, "switch", "--tag", "v1").ExitCode).To(Equal(0))

		writeText(dir, "a.txt", "tweak\n")
		res := runNipa(dir, "shelve")
		id := shelveID(res)
		Expect(res.Stdout).To(ContainSubstring(`"1 file on v1"`))
		Expect(runNipa(dir, "shelve", "list").Output()).To(ContainSubstring("tag v1"))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "shelve", "apply", id).ExitCode).To(Equal(0))
		Expect(readText(dir, "a.txt")).To(Equal("tweak\n"))
		Expect(readText(dir, "b.txt")).To(Equal("b\n"))
	})

	It("works in a sparse clone and refuses files outside the checkout", func() {
		project := newProject("shelve-sparse")
		seedRepo(orgSlug, project, map[string][]byte{"src/a.txt": []byte("a\n"), "docs/d.txt": []byte("d\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work", "--sparse", "src,docs")
		writeText(dir, "docs/d.txt", "edited\n")
		id := shelveID(runNipa(dir, "shelve"))
		Expect(readText(dir, "docs/d.txt")).To(Equal("d\n"))

		Expect(runNipa(dir, "sparse-checkout", "set", "src").ExitCode).To(Equal(0))
		res := runNipa(dir, "shelve", "apply", id)
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("outside this clone's checkout"))

		Expect(runNipa(dir, "sparse-checkout", "add", "docs").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "shelve", "apply", id).ExitCode).To(Equal(0))
		Expect(readText(dir, "docs/d.txt")).To(Equal("edited\n"))
	})

	It("refuses merge and revert while an apply waits", func() {
		project := newProject("shelve-blocks")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "mine\n")
		id := shelveID(runNipa(dir, "shelve"))
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("theirs\n")}, "upstream")
		Expect(runNipa(dir, "update").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "shelve", "apply", id).Output()).To(ContainSubstring("stopped on conflicts"))

		for _, args := range [][]string{{"merge", "main"}, {"revert", "HEAD"}, {"shelve"}} {
			res := runNipa(dir, args...)
			Expect(res.ExitCode).NotTo(Equal(0), args)
			Expect(res.Output()).To(ContainSubstring("stopped on conflicts"), args)
		}
		Expect(runNipa(dir, "gc").Output()).To(ContainSubstring("skipped"))
		Expect(runNipa(dir, "shelve", "apply", "--abort").ExitCode).To(Equal(0))
	})
})

var _ = Describe("nipa serve shelves", func() {
	It("lists staged files a switch would block on", func() {
		project := newProject("shelve-syncplan-switch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "f\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature").ExitCode).To(Equal(0))

		writeText(dir, "staged.txt", "s\n")
		Expect(runNipa(dir, "add", "staged.txt").ExitCode).To(Equal(0))
		writeText(dir, "a.txt", "local\n")
		d := startDaemon()
		d.watch(dir)

		plan, err := d.client.SyncPlan(d.ctx, &daemonpb.SyncPlanRequest{Root: dir, Branch: "main"})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.GetOverwritten()).To(Equal([]string{"staged.txt"}), "a.txt is the same on main, the staged file blocks the switch")

		stream, err := d.client.ShelveCreate(d.ctx, &daemonpb.ShelveCreateRequest{Root: dir, Name: "Before switching to main", Paths: plan.GetOverwritten()})
		Expect(err).NotTo(HaveOccurred())
		Expect(opResult(collectOpEvents(stream)).GetShelve()).NotTo(BeNil())
		sw, err := d.client.Switch(d.ctx, &daemonpb.SwitchRequest{Root: dir, Branch: "main"})
		Expect(err).NotTo(HaveOccurred())
		Expect(opResult(collectOpEvents(sw))).NotTo(BeNil())
		Expect(readText(dir, "a.txt")).To(Equal("local\n"), "an edit main does not touch survives the switch")
		Expect(fileExists(dir, "staged.txt")).To(BeFalse())
	})

	It("serves shelves over the JSON bridge", func() {
		project := newProject("shelve-json")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		d := startDaemon()
		body := func(v map[string]any) string {
			data, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			return string(data)
		}
		resp, _ := d.jsonPost("/v1/WatchRepo", body(map[string]any{"root": dir}))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		writeText(dir, "a.txt", "plugin edit\n")
		resp, out := d.jsonPost("/v1/ShelveCreate", body(map[string]any{"root": dir, "name": "from plugin"}))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		Expect(lines[len(lines)-1]).To(ContainSubstring(`"from plugin"`))
		Expect(readText(dir, "a.txt")).To(Equal("a\n"))

		resp, out = d.jsonPost("/v1/ShelveList", body(map[string]any{"root": dir}))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var list struct {
			Shelves []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"shelves"`
		}
		Expect(json.Unmarshal(out, &list)).To(Succeed())
		Expect(list.Shelves).To(HaveLen(1))

		resp, out = d.jsonPost("/v1/ReadBlob", body(map[string]any{"root": dir, "blob": map[string]any{"source": "shelf", "revision": list.Shelves[0].ID, "path": "a.txt"}}))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(string(out)).To(ContainSubstring(`"data"`))

		resp, _ = d.jsonPost("/v1/ShelveApply", body(map[string]any{"root": dir, "id": list.Shelves[0].ID}))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(readText(dir, "a.txt")).To(Equal("plugin edit\n"))

		resp, _ = d.jsonPost("/v1/ShelveDrop", body(map[string]any{"root": dir, "id": list.Shelves[0].ID}))
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound), "a clean apply already dropped it")
	})
})

func mustReadBlob(d *daemonProc, root string, ref *daemonpb.BlobRef) daemonpb.NipaDaemon_ReadBlobClient {
	GinkgoHelper()
	stream, err := d.client.ReadBlob(d.ctx, &daemonpb.ReadBlobRequest{Root: root, Blob: ref})
	Expect(err).NotTo(HaveOccurred())
	return stream
}
