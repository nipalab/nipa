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
		Expect(limited.Output()).To(ContainSubstring("more results: --after 2"))

		var limitedJSON mergeRequestsJSON
		runJSONInto(dir, &limitedJSON, "mr", "list", "--limit", "1", "--json")
		Expect(limitedJSON.MergeRequests).To(HaveLen(1))
		Expect(limitedJSON.MergeRequests[0].SourceBranch).To(Equal("feature-b"))
		Expect(limitedJSON.NextCursor).To(Equal("2"))

		var nextPage mergeRequestsJSON
		runJSONInto(dir, &nextPage, "mr", "list", "--limit", "1", "--after", limitedJSON.NextCursor, "--json")
		Expect(nextPage.MergeRequests).To(HaveLen(1))
		Expect(nextPage.MergeRequests[0].SourceBranch).To(Equal("feature-a"))
		Expect(nextPage.NextCursor).To(BeEmpty())

		bySource := runNipa(dir, "mr", "list", "--source", "feature-a")
		Expect(bySource.Output()).To(ContainSubstring("feature-a"))
		Expect(bySource.Output()).NotTo(ContainSubstring("feature-b"))

		byTarget := runNipa(dir, "mr", "list", "--target", "main")
		Expect(byTarget.Output()).To(ContainSubstring("feature-a"))
		Expect(byTarget.Output()).To(ContainSubstring("feature-b"))

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

		unknownView := runNipa(dir, "mr", "view", "99")
		Expect(unknownView.ExitCode).To(Equal(127))

		unknownReview := runNipa(dir, "mr", "review", "99", "--approve", "-m", "x")
		Expect(unknownReview.ExitCode).To(Equal(127))

		unknownReopen := runNipa(dir, "mr", "reopen", "99")
		Expect(unknownReopen.ExitCode).To(Equal(127))

		unknownDiff := runNipa(dir, "mr", "diff", "99")
		Expect(unknownDiff.ExitCode).To(Equal(127))

		noMessage := runNipa(dir, "mr", "review", "1", "--approve")
		Expect(noMessage.ExitCode).To(Equal(1))
		Expect(noMessage.Output()).To(ContainSubstring("needs a message"))

		bothFlags := runNipa(dir, "mr", "review", "1", "--approve", "--request-changes", "-m", "x")
		Expect(bothFlags.ExitCode).To(Equal(1))
		Expect(bothFlags.Output()).To(ContainSubstring("not both"))

		unanchored := runNipa(dir, "mr", "comment", "1", "-m", "x", "--new-line", "2")
		Expect(unanchored.ExitCode).To(Equal(1))
		Expect(unanchored.Output()).To(ContainSubstring("pass --file"))
	})

	It("tracks draft state and refuses to merge drafts", func() {
		project := newProject("mr-draft")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))

		res := runNipa(dir, "mr", "create", "--title", uniqueMessage("Draft work"), "--draft")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("[draft]"))

		var view mergeRequestViewJSON
		runJSONInto(dir, &view, "mr", "view", "1", "--json")
		Expect(view.MergeRequest.Draft).To(BeTrue())
		Expect(view.Mergeability).NotTo(BeNil())
		Expect(view.Mergeability.BlockedBy).To(Equal("draft"))

		blocked := runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))
		Expect(blocked.Output()).To(ContainSubstring("draft"))

		drafts := runNipa(dir, "mr", "list", "--status", "draft")
		Expect(drafts.ExitCode).To(Equal(0), drafts.Output())
		Expect(drafts.Output()).To(ContainSubstring("draft"))
		Expect(drafts.Output()).To(ContainSubstring("Draft work"))

		ready := runNipa(dir, "mr", "ready", "1")
		Expect(ready.ExitCode).To(Equal(0), ready.Output())
		Expect(ready.Output()).To(ContainSubstring("ready for review"))

		merged := runNipa(dir, "mr", "merge", "1")
		Expect(merged.ExitCode).To(Equal(0), merged.Output())
		Expect(merged.Output()).To(ContainSubstring("merged"))
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
		protection := api.setBranchProtection(orgSlug, project, "main", true, &one, nil)
		Expect(protection.RequiredApprovals).To(Equal(int64(1)))

		check := api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.Status).To(Equal("mergeable"))
		Expect(check.BlockedBy).To(Equal("insufficient_approvals"))

		blockedView := runNipa(dir, "mr", "view", "1")
		Expect(blockedView.Output()).To(ContainSubstring("blocked by insufficient_approvals"))

		blocked := runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))
		Expect(blocked.Output()).To(ContainSubstring("approvals"))

		reviewerAPI.submitMergeRequestReview(orgSlug, project, 1, "changes_requested", "not yet")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("changes_requested"))
		blockedView = runNipa(dir, "mr", "view", "1")
		Expect(blockedView.Output()).To(ContainSubstring("blocked by changes_requested"))
		blocked = runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))
		Expect(blocked.Output()).To(ContainSubstring("change requests"))

		reviewerAPI.submitMergeRequestReview(orgSlug, project, 1, "approved", "ok")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(BeEmpty())

		merge := runNipa(dir, "mr", "merge", "1")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())
		Expect(merge.Output()).To(ContainSubstring("merged"))

		var timeline mergeRequestTimelineJSON
		runJSONInto(dir, &timeline, "mr", "timeline", "1", "--json")
		kinds := make([]string, 0, len(timeline.Timeline))
		for _, item := range timeline.Timeline {
			kinds = append(kinds, item.Kind)
		}
		Expect(kinds).To(ContainElement("opened"))
		Expect(kinds).To(ContainElement("review_submitted"))
		Expect(kinds).To(ContainElement("merged"))

		// the list carries the live review summary and filters by author
		var list mergeRequestsJSON
		runJSONInto(dir, &list, "mr", "list", "--status", "merged", "--json")
		Expect(list.MergeRequests).To(HaveLen(1))
		Expect(list.MergeRequests[0].Review).NotTo(BeNil())
		Expect(list.MergeRequests[0].Review.Approvals).To(Equal(1))

		byAuthor := runNipa(dir, "mr", "list", "--author", list.MergeRequests[0].CreatedBy)
		Expect(byAuthor.ExitCode).To(Equal(0), byAuthor.Output())
		Expect(byAuthor.Output()).To(ContainSubstring("feature -> main"))
		Expect(byAuthor.Output()).To(ContainSubstring("Gated MR"))
	})

	It("keeps approvals across pushes when stale dismissal is disabled", func() {
		project := newProject("mr-keep-approvals")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Keep approvals")).ExitCode).To(Equal(0))

		reviewer := newIdentity("mr-keep-reviewer").promote()
		reviewerAPI := newAPIClient(cli.apiURL, reviewer.email, reviewer.password)

		one := int64(1)
		off := false
		protection := api.setBranchProtection(orgSlug, project, "main", true, &one, &off)
		Expect(protection.RequiredApprovals).To(Equal(int64(1)))
		Expect(protection.DismissStaleApprovals).To(BeFalse())

		reviewerAPI.submitMergeRequestReview(orgSlug, project, 1, "approved", "ok")
		check := api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(BeEmpty())

		// the second push carries the approval onto the new head
		writeText(dir, "more.txt", "more\n")
		Expect(runNipa(dir, "add", "more.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "more work").ExitCode).To(Equal(0))

		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(BeEmpty())

		merge := runNipa(dir, "mr", "merge", "1")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())
		Expect(merge.Output()).To(ContainSubstring("merged"))
	})

	It("reviews, comments and resolves threads from the CLI", func() {
		project := newProject("mr-review-cli")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Review CLI")).ExitCode).To(Equal(0))

		reviewer := newIdentity("mr-cli-reviewer").promote()
		reviewerAPI := newAPIClient(cli.apiURL, reviewer.email, reviewer.password)

		view := runNipaAs(reviewer, dir, "mr", "view", "1")
		Expect(view.ExitCode).To(Equal(0), view.Output())
		Expect(view.Output()).To(ContainSubstring("#1"))
		Expect(view.Output()).To(ContainSubstring("branches: feature -> main"))

		var viewJSON mergeRequestViewJSON
		runJSONIntoAs(reviewer, dir, &viewJSON, "mr", "view", "1", "--json")
		Expect(viewJSON.MergeRequest.Number).To(Equal(int64(1)))
		Expect(viewJSON.Mergeability).NotTo(BeNil())
		Expect(viewJSON.Mergeability.Status).To(Equal("mergeable"))
		Expect(viewJSON.Commits).NotTo(BeEmpty())

		review := runNipaAs(reviewer, dir, "mr", "review", "1", "--request-changes", "-m", "please fix")
		Expect(review.ExitCode).To(Equal(0), review.Output())
		Expect(review.Output()).To(ContainSubstring("reviewed: changes_requested"))

		view = runNipaAs(reviewer, dir, "mr", "view", "1")
		Expect(view.Output()).To(ContainSubstring("changes_requested"))
		Expect(view.Output()).To(ContainSubstring("outstanding reviewers"))

		runJSONIntoAs(reviewer, dir, &viewJSON, "mr", "view", "1", "--json")
		Expect(viewJSON.Reviews).To(HaveLen(1))
		Expect(viewJSON.Reviews[0].State).To(Equal("changes_requested"))

		comment := runNipaAs(reviewer, dir, "mr", "comment", "1", "-m", "overall note")
		Expect(comment.ExitCode).To(Equal(0), comment.Output())
		Expect(comment.Output()).To(ContainSubstring("thread"))

		var threads mergeRequestThreadsJSON
		runJSONIntoAs(reviewer, dir, &threads, "mr", "comments", "1", "--json")
		Expect(threads.Threads).To(HaveLen(1))
		threadID := threads.Threads[0].ID
		Expect(threadID).NotTo(BeEmpty())

		inline := runNipaAs(reviewer, dir, "mr", "comment", "1", "-m", "rename this", "--file", "feature.txt", "--new-line", "1")
		Expect(inline.ExitCode).To(Equal(0), inline.Output())

		reply := runNipaAs(reviewer, dir, "mr", "reply", "1", threadID, "-m", "done")
		Expect(reply.ExitCode).To(Equal(0), reply.Output())
		Expect(reply.Output()).To(ContainSubstring("added to thread"))

		resolve := runNipaAs(reviewer, dir, "mr", "resolve", "1", threadID)
		Expect(resolve.ExitCode).To(Equal(0), resolve.Output())
		Expect(resolve.Output()).To(ContainSubstring("resolved"))

		runJSONIntoAs(reviewer, dir, &threads, "mr", "comments", "1", "--json")
		Expect(threads.Threads).To(HaveLen(2))
		resolved := false
		for _, thread := range threads.Threads {
			if thread.ID == threadID {
				resolved = thread.Resolved
			}
		}
		Expect(resolved).To(BeTrue())

		diff := runNipaAs(reviewer, dir, "mr", "diff", "1")
		Expect(diff.ExitCode).To(Equal(0), diff.Output())
		Expect(diff.Output()).To(ContainSubstring("feature.txt"))
		Expect(diff.Output()).To(ContainSubstring("+feature"))

		var diffJSON mergeRequestDiffJSON
		runJSONIntoAs(reviewer, dir, &diffJSON, "mr", "diff", "1", "--json")
		Expect(diffJSON.Files).To(HaveLen(1))
		Expect(diffJSON.Files[0].Path).To(Equal("feature.txt"))
		Expect(diffJSON.Files[0].Additions).To(BeNumerically(">=", 1))
		Expect(diffJSON.Files[0].Patch).NotTo(BeEmpty())

		var timeline mergeRequestTimelineJSON
		runJSONIntoAs(reviewer, dir, &timeline, "mr", "timeline", "1", "--json")
		kinds := make([]string, 0, len(timeline.Timeline))
		for _, item := range timeline.Timeline {
			kinds = append(kinds, item.Kind)
		}
		Expect(kinds).To(ContainElement("opened"))
		Expect(kinds).To(ContainElement("review_submitted"))

		note := runNipaAs(reviewer, dir, "mr", "review", "1", "-m", "note only")
		Expect(note.ExitCode).To(Equal(0), note.Output())
		Expect(note.Output()).To(ContainSubstring("reviewed: commented"))

		// a review submitted over the REST API can carry inline comments
		reviewerAPI.submitMergeRequestReviewWithComment(orgSlug, project, 1, "commented", "inline review", "feature.txt", 1)
		runJSONIntoAs(reviewer, dir, &threads, "mr", "comments", "1", "--json")
		Expect(threads.Threads).To(HaveLen(3))

		// REST-only follow-ups with no CLI command: edit/delete a comment,
		// delete a thread and withdraw a review.
		var topThreadID, topCommentID string
		for _, thread := range threads.Threads {
			if thread.FilePath == "" && len(thread.Comments) > 0 {
				topThreadID = thread.ID
				topCommentID = thread.Comments[0].ID
			}
		}
		Expect(topThreadID).NotTo(BeEmpty())
		Expect(topCommentID).NotTo(BeEmpty())

		reviewerAPI.updateMergeRequestComment(orgSlug, project, 1, topThreadID, topCommentID, "edited note")
		runJSONIntoAs(reviewer, dir, &threads, "mr", "comments", "1", "--json")
		edited := ""
		for _, thread := range threads.Threads {
			if thread.ID == topThreadID && len(thread.Comments) > 0 {
				edited = thread.Comments[0].Body
			}
		}
		Expect(edited).To(Equal("edited note"))

		reviewerAPI.deleteMergeRequestComment(orgSlug, project, 1, topThreadID, topCommentID)
		reviewerAPI.deleteMergeRequestThread(orgSlug, project, 1, topThreadID)
		runJSONIntoAs(reviewer, dir, &threads, "mr", "comments", "1", "--json")
		Expect(threads.Threads).To(HaveLen(2))

		withdrawnID := reviewerAPI.submitMergeRequestReview(orgSlug, project, 1, "commented", "to withdraw")
		viewJSON = mergeRequestViewJSON{}
		runJSONIntoAs(reviewer, dir, &viewJSON, "mr", "view", "1", "--json")
		Expect(viewJSON.Reviews).To(HaveLen(1))
		reviewerAPI.withdrawMergeRequestReview(orgSlug, project, 1, withdrawnID)
		viewJSON = mergeRequestViewJSON{}
		runJSONIntoAs(reviewer, dir, &viewJSON, "mr", "view", "1", "--json")
		Expect(viewJSON.Reviews).To(BeEmpty())

		approve := runNipaAs(reviewer, dir, "mr", "review", "1", "--approve", "-m", "looks good")
		Expect(approve.ExitCode).To(Equal(0), approve.Output())
		Expect(approve.Output()).To(ContainSubstring("reviewed: approved"))
	})

	It("requests reviews and reopens from the CLI", func() {
		project := newProject("mr-requests-cli")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Requests CLI")).ExitCode).To(Equal(0))

		reviewer := newIdentity("mr-requests-reviewer").promote()

		request := runNipa(dir, "mr", "request-review", "1", reviewer.userID)
		Expect(request.ExitCode).To(Equal(0), request.Output())
		Expect(request.Output()).To(ContainSubstring("review requested from"))

		requests := runNipa(dir, "mr", "requests", "1")
		Expect(requests.ExitCode).To(Equal(0), requests.Output())
		Expect(requests.Output()).To(ContainSubstring("requested from"))

		unrequest := runNipa(dir, "mr", "unrequest-review", "1", reviewer.userID)
		Expect(unrequest.ExitCode).To(Equal(0), unrequest.Output())
		Expect(unrequest.Output()).To(ContainSubstring("removed"))

		empty := runNipa(dir, "mr", "requests", "1")
		Expect(empty.Output()).To(ContainSubstring("no review requests"))

		Expect(runNipa(dir, "mr", "close", "1").ExitCode).To(Equal(0))
		reopen := runNipa(dir, "mr", "reopen", "1")
		Expect(reopen.ExitCode).To(Equal(0), reopen.Output())
		Expect(reopen.Output()).To(ContainSubstring("reopened"))

		var timeline mergeRequestTimelineJSON
		runJSONInto(dir, &timeline, "mr", "timeline", "1", "--json")
		kinds := make([]string, 0, len(timeline.Timeline))
		for _, item := range timeline.Timeline {
			kinds = append(kinds, item.Kind)
		}
		Expect(kinds).To(ContainElement("review_requested"))
		Expect(kinds).To(ContainElement("review_request_removed"))
		Expect(kinds).To(ContainElement("closed"))
		Expect(kinds).To(ContainElement("reopened"))

		view := runNipa(dir, "mr", "view", "1")
		Expect(view.Output()).To(ContainSubstring("status: open"))
	})
})
