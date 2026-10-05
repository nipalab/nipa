package suite_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa mr", func() {
	It("creates a merge request from the current branch by default", func() {
		project := newProject("mr-create")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))

		res := runNipa(dir, "mr", "create", "--title", "Add feature", "-d", "the description")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Merge request #1 opened: feature -> main (Add feature)"))

		list := runNipa(dir, "mr", "list")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("feature -> main"))
		Expect(list.Output()).To(ContainSubstring("open"))
		Expect(list.Output()).To(ContainSubstring("Add feature"))

		var out mergeRequestsJSON
		runJSONInto(dir, &out, "mr", "list", "--json")
		Expect(out.MergeRequests).To(HaveLen(1))
		mr := out.MergeRequests[0]
		Expect(mr.Number).To(Equal(int64(1)))
		Expect(mr.SourceBranch).To(Equal("feature"))
		Expect(mr.TargetBranch).To(Equal("main"))
		Expect(mr.Title).To(Equal("Add feature"))
		Expect(mr.Description).To(Equal("the description"))
		Expect(mr.Status).To(Equal("open"))
	})

	It("creates a merge request with explicit source and target", func() {
		project := newProject("mr-explicit")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		feature := cloneRepo(parent, url, "feature")
		Expect(runNipa(feature, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(feature, "feature.txt", "feature\n")
		Expect(runNipa(feature, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(feature, "feature work").ExitCode).To(Equal(0))

		mainClone := cloneRepo(parent, url, "main")
		res := runNipa(mainClone, "mr", "create", "--title", "Explicit", "--source", "feature", "--target", "main")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		var out mergeRequestsJSON
		runJSONInto(mainClone, &out, "mr", "list", "--json")
		Expect(out.MergeRequests).To(HaveLen(1))
		Expect(out.MergeRequests[0].SourceBranch).To(Equal("feature"))
		Expect(out.MergeRequests[0].TargetBranch).To(Equal("main"))
	})

	It("updates title and description", func() {
		project := newProject("mr-update")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "Before").ExitCode).To(Equal(0))

		res := runNipa(dir, "mr", "update", "1", "--title", "After", "-d", "new description")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Merge request #1 updated: After"))

		var out mergeRequestsJSON
		runJSONInto(dir, &out, "mr", "list", "--json")
		Expect(out.MergeRequests[0].Title).To(Equal("After"))
		Expect(out.MergeRequests[0].Description).To(Equal("new description"))
	})

	It("filters and limits the list", func() {
		project := newProject("mr-list")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		for _, name := range []string{"feature-a", "feature-b"} {
			Expect(runNipa(dir, "branch", "-c", name).ExitCode).To(Equal(0))
			writeText(dir, name+".txt", name+"\n")
			Expect(runNipa(dir, "add", name+".txt").ExitCode).To(Equal(0))
			Expect(pushRepo(dir, name+" work").ExitCode).To(Equal(0))
			Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("MR "+name)).ExitCode).To(Equal(0))
			Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		}

		open := runNipa(dir, "mr", "list", "--status", "open")
		Expect(open.ExitCode).To(Equal(0), open.Output())
		Expect(open.Output()).To(ContainSubstring("feature-a"))
		Expect(open.Output()).To(ContainSubstring("feature-b"))

		limited := runNipa(dir, "mr", "list", "--limit", "1")
		Expect(limited.ExitCode).To(Equal(0), limited.Output())
		Expect(limited.Output()).To(ContainSubstring("feature-b"))
		Expect(limited.Output()).NotTo(ContainSubstring("feature-a"))

		Expect(runNipa(dir, "mr", "merge", "1").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "close", "2").ExitCode).To(Equal(0))

		merged := runNipa(dir, "mr", "list", "--status", "merged")
		Expect(merged.Output()).To(ContainSubstring("feature-a"))
		Expect(merged.Output()).NotTo(ContainSubstring("feature-b"))

		closed := runNipa(dir, "mr", "list", "--status", "closed")
		Expect(closed.Output()).To(ContainSubstring("feature-b"))
		Expect(closed.Output()).NotTo(ContainSubstring("feature-a"))

		none := runNipa(dir, "mr", "list", "--status", "open")
		Expect(none.Output()).To(ContainSubstring("no merge requests"))
	})

	It("closes a merge request", func() {
		project := newProject("mr-close")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "To close").ExitCode).To(Equal(0))

		res := runNipa(dir, "mr", "close", "1")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Merge request #1 closed."))

		var out mergeRequestsJSON
		runJSONInto(dir, &out, "mr", "list", "--json")
		Expect(out.MergeRequests[0].Status).To(Equal("closed"))

		merge := runNipa(dir, "mr", "merge", "1")
		Expect(merge.ExitCode).NotTo(Equal(0))
	})

	It("merges a fast-forwardable request", func() {
		project := newProject("mr-merge-ff")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("FF merge")).ExitCode).To(Equal(0))

		res := runNipa(dir, "mr", "merge", "1")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("Merge request #1 merged: feature -> main"))

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "feature.txt")).To(Equal("feature\n"))

		var out mergeRequestsJSON
		runJSONInto(dir, &out, "mr", "list", "--json")
		Expect(out.MergeRequests[0].Status).To(Equal("merged"))
	})

	It("refuses to merge when the source is behind and succeeds after catching up", func() {
		project := newProject("mr-merge-diverged")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Diverged merge")).ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"main.txt": []byte("main\n")}, "move main")

		behind := runNipa(dir, "mr", "merge", "1")
		Expect(behind.ExitCode).To(Equal(2))
		Expect(behind.Output()).To(ContainSubstring("source branch is behind the target"))

		catchUp := runNipa(dir, "merge", "-m", uniqueMessage("catch up main"), "main")
		Expect(catchUp.ExitCode).To(Equal(0), catchUp.Output())

		res := runNipa(dir, "mr", "merge", "1")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "feature.txt")).To(Equal("feature\n"))
		Expect(readText(fresh, "main.txt")).To(Equal("main\n"))
	})

	It("keeps the request open when the source branch is pushed again", func() {
		project := newProject("mr-source-push")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "v1\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "Growing").ExitCode).To(Equal(0))

		writeText(dir, "feature.txt", "v2\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature follow-up").ExitCode).To(Equal(0))

		var out mergeRequestsJSON
		runJSONInto(dir, &out, "mr", "list", "--json")
		Expect(out.MergeRequests).To(HaveLen(1))
		Expect(out.MergeRequests[0].Status).To(Equal("open"))
	})

	It("releases merge-request locks on merge", func() {
		project := newProject("mr-binary-locks")
		seedRepo(orgSlug, project, map[string][]byte{"blob.bin": []byte{0x00, 0x01, 0x02}}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("mr-writer").promote()

		dir := cloneRepo(workspace(), url, "writer")
		Expect(runNipaAs(writer, dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		Expect(runNipaAs(writer, dir, "lock", "blob.bin").ExitCode).To(Equal(0))
		writeBytes(dir, "blob.bin", []byte{0x00, 0x09, 0x08})
		Expect(runNipaAs(writer, dir, "add", "blob.bin").ExitCode).To(Equal(0))
		push := runNipaAs(writer, dir, "push", "-m", uniqueMessage("binary feature"))
		Expect(push.ExitCode).To(Equal(0), push.Output())

		create := runNipaAs(writer, dir, "mr", "create", "--title", uniqueMessage("Binary MR"))
		Expect(create.ExitCode).To(Equal(0), create.Output())

		adminDir := cloneRepo(workspace(), url, "admin")
		var locked locksJSON
		runJSONInto(adminDir, &locked, "lock", "list", "--json")
		Expect(locked.Locks).To(HaveLen(1))
		Expect(locked.Locks[0].Scope).To(Equal("mainline"))
		Expect(locked.Locks[0].MergeRequestNumber).NotTo(BeNil())
		Expect(*locked.Locks[0].MergeRequestNumber).To(Equal(int64(1)))

		merge := runNipa(adminDir, "mr", "merge", "1")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())

		var released locksJSON
		runJSONInto(adminDir, &released, "lock", "list", "--json")
		Expect(released.Locks).To(BeEmpty())

		fresh := cloneRepo(workspace(), url, "fresh")
		Expect(readBytes(fresh, "blob.bin")).To(Equal([]byte{0x00, 0x09, 0x08}))
	})

	It("rejects invalid merge request operations", func() {
		project := newProject("mr-errors")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		noTitle := runNipa(dir, "mr", "create")
		Expect(noTitle.ExitCode).To(Equal(1))
		Expect(noTitle.Output()).To(ContainSubstring("title is required"))

		same := runNipa(dir, "mr", "create", "--title", "same")
		Expect(same.ExitCode).To(Equal(1))
		Expect(same.Output()).To(ContainSubstring("source and target branches must differ"))

		unknownUpdate := runNipa(dir, "mr", "update", "99", "--title", "x")
		Expect(unknownUpdate.ExitCode).To(Equal(127))

		unknownClose := runNipa(dir, "mr", "close", "99")
		Expect(unknownClose.ExitCode).To(Equal(127))

		unknownMerge := runNipa(dir, "mr", "merge", "99")
		Expect(unknownMerge.ExitCode).To(Equal(127))
	})

	It("gates merging on required approvals and change requests", func() {
		project := newProject("mr-gate")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Gated MR")).ExitCode).To(Equal(0))

		reviewer := newIdentity("mr-gate-reviewer").promote()
		reviewerAPI := newAPIClient(cli.apiURL, reviewer.email, reviewer.password)

		one := int64(1)
		protection := api.setBranchProtection(orgSlug, project, "main", true, &one)
		Expect(protection.RequiredApprovals).To(Equal(int64(1)))

		check := api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.Status).To(Equal("mergeable"))
		Expect(check.BlockedBy).To(Equal("insufficient_approvals"))

		blocked := runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))
		Expect(blocked.Output()).To(ContainSubstring("approvals"))

		reviewerAPI.submitMergeRequestReview(orgSlug, project, 1, "changes_requested", "not yet")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("changes_requested"))
		blocked = runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))
		Expect(blocked.Output()).To(ContainSubstring("change requests"))

		reviewerAPI.submitMergeRequestReview(orgSlug, project, 1, "approved", "ok")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(BeEmpty())

		merge := runNipa(dir, "mr", "merge", "1")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())
		Expect(merge.Output()).To(ContainSubstring("merged"))
	})
})
