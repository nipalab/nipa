package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa lock", func() {
	It("locks a path on the default branch and lists it", func() {
		project := newProject("lock-basic")
		seedRepo(orgSlug, project, map[string][]byte{"assets/hero.png": []byte{0x00, 0x01, 0x02}}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "lock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Locked assets/hero.png (mainline)"))

		list := runNipa(dir, "lock", "list")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("assets/hero.png"))
		Expect(list.Output()).To(ContainSubstring("mainline"))
		Expect(list.Output()).To(ContainSubstring("Super Admin"))

		var out locksJSON
		runJSONInto(dir, &out, "lock", "list", "--json")
		Expect(out.Locks).To(HaveLen(1))
		Expect(out.Locks[0].Path).To(Equal("assets/hero.png"))
		Expect(out.Locks[0].Scope).To(Equal("mainline"))
		Expect(out.Locks[0].HeldByName).To(Equal("Super Admin"))
	})

	It("is idempotent for the same holder", func() {
		project := newProject("lock-idempotent")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "lock", "assets/hero.png").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "lock", "assets/hero.png").ExitCode).To(Equal(0))

		var out locksJSON
		runJSONInto(dir, &out, "lock", "list", "--json")
		Expect(out.Locks).To(HaveLen(1))
	})

	It("rejects another holder", func() {
		project := newProject("lock-conflict")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-writer").promote()

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "lock", "assets/hero.png").ExitCode).To(Equal(0))

		writerDir := cloneRepo(workspace(), url, "writer")
		res := runNipaAs(writer, writerDir, "lock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(2))
		Expect(res.Output()).To(ContainSubstring("is locked by"))
	})

	It("covers files with a directory lock", func() {
		project := newProject("lock-directory")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-dir-writer").promote()

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "lock", "assets").ExitCode).To(Equal(0))

		writerDir := cloneRepo(workspace(), url, "writer")
		res := runNipaAs(writer, writerDir, "lock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(2))
		Expect(res.Output()).To(ContainSubstring("is locked by"))

		other := runNipaAs(writer, writerDir, "lock", "src/main.go")
		Expect(other.ExitCode).To(Equal(0), other.Output())
	})

	It("releases locks with unlock", func() {
		project := newProject("lock-unlock")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-unlock-writer").promote()

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "lock", "assets/hero.png").ExitCode).To(Equal(0))

		res := runNipa(adminDir, "unlock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Unlocked assets/hero.png"))

		writerDir := cloneRepo(workspace(), url, "writer")
		relock := runNipaAs(writer, writerDir, "lock", "assets/hero.png")
		Expect(relock.ExitCode).To(Equal(0), relock.Output())
	})

	It("fails to unlock a path without a lock", func() {
		project := newProject("lock-unlock-missing")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "unlock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(127))
		Expect(res.Output()).To(ContainSubstring("no lock on"))
	})

	It("requires a lock before pushing a tracked binary change", func() {
		project := newProject("lock-tracked-binary")
		seedRepo(orgSlug, project, map[string][]byte{"blob.bin": []byte{0x00, 0x01, 0x02}}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-binary-writer").promote()
		dir := cloneRepo(workspace(), url, "writer")

		writeBytes(dir, "blob.bin", []byte{0x00, 0x09, 0x08})
		Expect(runNipaAs(writer, dir, "add", "blob.bin").ExitCode).To(Equal(0))

		denied := runNipaAs(writer, dir, "push", "-m", uniqueMessage("binary edit"))
		Expect(denied.ExitCode).To(Equal(2))
		Expect(denied.Output()).To(ContainSubstring("requires a lock"))

		Expect(runNipaAs(writer, dir, "lock", "blob.bin").ExitCode).To(Equal(0))
		push := runNipaAs(writer, dir, "push", "-m", uniqueMessage("binary edit"))
		Expect(push.ExitCode).To(Equal(0), push.Output())

		fresh := cloneRepo(workspace(), url, "fresh")
		Expect(readBytes(fresh, "blob.bin")).To(Equal([]byte{0x00, 0x09, 0x08}))
	})

	It("blocks a new binary behind someone else's directory lock", func() {
		project := newProject("lock-new-binary")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-new-writer").promote()

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(runNipa(adminDir, "lock", "assets").ExitCode).To(Equal(0))

		dir := cloneRepo(workspace(), url, "writer")
		writeBytes(dir, "assets/new.bin", []byte{0x00, 0x01})
		Expect(runNipaAs(writer, dir, "add", "assets/new.bin").ExitCode).To(Equal(0))

		denied := runNipaAs(writer, dir, "push", "-m", uniqueMessage("new binary"))
		Expect(denied.ExitCode).To(Equal(2))
		Expect(denied.Output()).To(ContainSubstring("is locked by"))

		Expect(runNipa(adminDir, "unlock", "assets").ExitCode).To(Equal(0))
		push := runNipaAs(writer, dir, "push", "-m", uniqueMessage("new binary"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
	})

	It("scopes locks to a branch", func() {
		project := newProject("lock-branch-scope")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-branch-writer").promote()

		dir := cloneRepo(workspace(), url, "writer")
		Expect(runNipaAs(writer, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))

		res := runNipaAs(writer, dir, "lock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`branch "feature"`))

		var out locksJSON
		runJSONInto(dir, &out, "lock", "list", "--json")
		Expect(out.Locks).To(HaveLen(1))
		Expect(out.Locks[0].Scope).To(Equal("branch"))
		Expect(out.Locks[0].Branch).To(Equal("feature"))

		adminDir := cloneRepo(workspace(), url, "admin")
		mainline := runNipa(adminDir, "lock", "assets/hero.png")
		Expect(mainline.ExitCode).To(Equal(0), mainline.Output())

		var both locksJSON
		runJSONInto(adminDir, &both, "lock", "list", "--json")
		Expect(both.Locks).To(HaveLen(2))

		unlock := runNipaAs(writer, dir, "unlock", "assets/hero.png", "--branch", "feature")
		Expect(unlock.ExitCode).To(Equal(0), unlock.Output())

		var remaining locksJSON
		runJSONInto(adminDir, &remaining, "lock", "list", "--json")
		Expect(remaining.Locks).To(HaveLen(1))
		Expect(remaining.Locks[0].Scope).To(Equal("mainline"))
	})

	It("lists no locks for a clean project", func() {
		project := newProject("lock-empty")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "lock", "list")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("no locks"))

		var out locksJSON
		runJSONInto(dir, &out, "lock", "list", "--json")
		Expect(out.Locks).To(BeEmpty())
	})
})
