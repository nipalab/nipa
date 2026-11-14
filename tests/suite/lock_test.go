package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa lock", func() {
	It("locks a path on the default branch and lists it", func() {
		project := newProject("lock-basic")
		seedRepo(orgSlug, project, map[string][]byte{"assets/hero.png": {0x00, 0x01, 0x02}}, "seed")
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
		seedRepo(orgSlug, project, map[string][]byte{"blob.bin": {0x00, 0x01, 0x02}}, "seed")
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

	It("releases branch-scoped locks when the branch is deleted", func() {
		project := newProject("lock-branch-delete")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("lock-delete-writer").promote()
		other := newIdentity("lock-delete-other").promote()

		dir := cloneRepo(workspace(), url, "writer")
		Expect(runNipaAs(writer, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		res := runNipaAs(writer, dir, "lock", "assets/hero.png")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring(`branch "feature"`))

		adminDir := cloneRepo(workspace(), url, "admin")
		mainline := runNipa(adminDir, "lock", "assets/hero.png")
		Expect(mainline.ExitCode).To(Equal(0), mainline.Output())

		var both locksJSON
		runJSONInto(adminDir, &both, "lock", "list", "--json")
		Expect(both.Locks).To(HaveLen(2))

		del := runNipa(adminDir, "branch", "-d", "feature")
		Expect(del.ExitCode).To(Equal(0), del.Output())

		var remaining locksJSON
		runJSONInto(adminDir, &remaining, "lock", "list", "--json")
		Expect(remaining.Locks).To(HaveLen(1), "the deleted branch's lock is released")
		Expect(remaining.Locks[0].Scope).To(Equal("mainline"))
		Expect(remaining.Locks[0].Path).To(Equal("assets/hero.png"))
		Expect(remaining.Locks[0].HeldByName).To(Equal("Super Admin"))

		otherDir := cloneRepoAs(other, workspace(), url, "other")
		Expect(runNipaAs(other, otherDir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		relock := runNipaAs(other, otherDir, "lock", "assets/hero.png")
		Expect(relock.ExitCode).To(Equal(0), relock.Output())
		Expect(relock.Output()).To(ContainSubstring(`branch "feature"`))

		var after locksJSON
		runJSONInto(adminDir, &after, "lock", "list", "--json")
		Expect(after.Locks).To(HaveLen(2))
		scopes := map[string]string{}
		for _, lock := range after.Locks {
			scopes[lock.Scope] = lock.HeldByName
		}
		Expect(scopes).To(HaveKeyWithValue("mainline", "Super Admin"))
		Expect(scopes).To(HaveKeyWithValue("branch", other.name))
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

var _ = Describe("nipa lock request", func() {
	It("asks the holder to release, emails them once and clears with the lock", func() {
		project := newProject("lock-request")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		holder := newIdentity("lock-req-holder").promote()
		asker := newIdentity("lock-req-asker").promote()

		holderDir := cloneRepoAs(holder, workspace(), url, "holder")
		Expect(runNipaAs(holder, holderDir, "lock", "assets/hero.png").ExitCode).To(Equal(0))
		askerDir := cloneRepoAs(asker, workspace(), url, "asker")

		res := runNipaAs(asker, askerDir, "lock", "request", "assets/hero.png", "-m", "need it for the trailer")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("to release assets/hero.png"))

		mail := email.waitForSubject(holder.email, "Release requested")
		Expect(mail.Subject).To(ContainSubstring("assets/hero.png"))
		Expect(mail.Text).To(ContainSubstring("asked you to release your lock on assets/hero.png"))
		Expect(mail.Text).To(ContainSubstring("need it for the trailer"))

		var out locksJSON
		runJSONInto(holderDir, &out, "lock", "list", "--json")
		Expect(out.Locks).To(HaveLen(1))
		Expect(out.Locks[0].Requests).To(HaveLen(1))
		Expect(out.Locks[0].Requests[0].Message).To(Equal("need it for the trailer"))

		again := runNipaAs(asker, askerDir, "lock", "request", "assets/hero.png", "-m", "still need it")
		Expect(again.ExitCode).To(Equal(0), again.Output())
		var updated locksJSON
		runJSONInto(holderDir, &updated, "lock", "list", "--json")
		Expect(updated.Locks[0].Requests).To(HaveLen(1), "one request per asker")
		Expect(updated.Locks[0].Requests[0].Message).To(Equal("still need it"))
		Consistently(func() int { return email.countFor(holder.email) }, "2s", "100ms").Should(Equal(1), "a repeat inside the cooldown does not email again")

		own := runNipaAs(holder, holderDir, "lock", "request", "assets/hero.png")
		Expect(own.ExitCode).NotTo(Equal(0))
		Expect(own.Output()).To(ContainSubstring("you hold this lock"))
		missing := runNipaAs(asker, askerDir, "lock", "request", "nothing/here.png")
		Expect(missing.ExitCode).NotTo(Equal(0))
		Expect(missing.Output()).To(ContainSubstring("is not locked"))

		Expect(runNipaAs(holder, holderDir, "unlock", "assets/hero.png").ExitCode).To(Equal(0))
		Expect(runNipaAs(holder, holderDir, "lock", "assets/hero.png").ExitCode).To(Equal(0))
		var relocked locksJSON
		runJSONInto(holderDir, &relocked, "lock", "list", "--json")
		Expect(relocked.Locks).To(HaveLen(1))
		Expect(relocked.Locks[0].Requests).To(BeEmpty(), "requests are removed with the lock")
	})
})
