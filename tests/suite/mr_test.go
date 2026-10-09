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

	It("merges a diverged source with a merge commit and combines text changes", func() {
		project := newProject("mr-strategy-merge")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("one\ntwo\nthree\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "base.txt", "one\ntwo\nthree-feature\n")
		Expect(runNipa(dir, "add", "base.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Strategy merge")).ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("one-main\ntwo\nthree\n")}, "move main")

		behind := runNipa(dir, "mr", "merge", "1")
		Expect(behind.ExitCode).To(Equal(2))

		merge := runNipa(dir, "mr", "merge", "1", "--strategy", "merge")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "base.txt")).To(Equal("one-main\ntwo\nthree-feature\n"))
		log := runNipa(fresh, "log")
		Expect(log.Output()).To(ContainSubstring("Merge branch 'feature' into main"))
	})

	It("squashes a diverged source into one commit", func() {
		project := newProject("mr-strategy-squash")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		title := uniqueMessage("Squashed work")
		Expect(runNipa(dir, "mr", "create", "--title", title).ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"main.txt": []byte("main\n")}, "move main")

		merge := runNipa(dir, "mr", "merge", "1", "--strategy", "squash")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "feature.txt")).To(Equal("feature\n"))
		Expect(readText(fresh, "main.txt")).To(Equal("main\n"))
		log := runNipa(fresh, "log")
		Expect(log.Output()).To(ContainSubstring(title))
		Expect(log.Output()).NotTo(ContainSubstring("Merge branch"))
	})

	It("rebases a diverged source onto the target", func() {
		project := newProject("mr-strategy-rebase")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "one.txt", "one\n")
		Expect(runNipa(dir, "add", "one.txt").ExitCode).To(Equal(0))
		first := uniqueMessage("rebase first")
		Expect(pushRepo(dir, first).ExitCode).To(Equal(0))
		writeText(dir, "two.txt", "two\n")
		Expect(runNipa(dir, "add", "two.txt").ExitCode).To(Equal(0))
		second := uniqueMessage("rebase second")
		Expect(pushRepo(dir, second).ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Rebase work")).ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"main.txt": []byte("main\n")}, "move main")

		merge := runNipa(dir, "mr", "merge", "1", "--strategy", "rebase")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "one.txt")).To(Equal("one\n"))
		Expect(readText(fresh, "two.txt")).To(Equal("two\n"))
		Expect(readText(fresh, "main.txt")).To(Equal("main\n"))
		log := runNipa(fresh, "log")
		Expect(log.Output()).To(ContainSubstring(first))
		Expect(log.Output()).To(ContainSubstring(second))
		Expect(log.Output()).NotTo(ContainSubstring("Merge branch"))
	})

	It("refuses a conflicting non-fast-forward merge", func() {
		project := newProject("mr-strategy-conflict")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "base.txt", "feature side\n")
		Expect(runNipa(dir, "add", "base.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Conflict work")).ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("main side\n")}, "move main")

		conflict := runNipa(dir, "mr", "merge", "1", "--strategy", "merge")
		Expect(conflict.ExitCode).To(Equal(2))
		Expect(conflict.Output()).To(ContainSubstring("merge conflicts in base.txt"))

		var out mergeRequestsJSON
		runJSONInto(dir, &out, "mr", "list", "--status", "open", "--json")
		Expect(out.MergeRequests).To(HaveLen(1))
	})

	It("deletes the source branch after merging", func() {
		project := newProject("mr-delete-source")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Delete source")).ExitCode).To(Equal(0))

		merge := runNipa(dir, "mr", "merge", "1", "--delete-source")
		Expect(merge.ExitCode).To(Equal(0), merge.Output())

		branches := runNipa(dir, "branch", "-a")
		Expect(branches.ExitCode).To(Equal(0), branches.Output())
		Expect(branches.Output()).NotTo(ContainSubstring("feature"))

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "feature.txt")).To(Equal("feature\n"))
	})

	It("merges over the REST API with a strategy and deletes the source", func() {
		project := newProject("mr-rest-strategy")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("REST strategy")).ExitCode).To(Equal(0))

		seedRepo(orgSlug, project, map[string][]byte{"main.txt": []byte("main\n")}, "move main")

		merged := api.mergeMergeRequest(orgSlug, project, 1, "squash", true)
		Expect(merged.Status).To(Equal("merged"))

		branches := runNipa(dir, "branch", "-a")
		Expect(branches.Output()).NotTo(ContainSubstring("feature"))

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "feature.txt")).To(Equal("feature\n"))
		Expect(readText(fresh, "main.txt")).To(Equal("main\n"))
	})

	It("refuses a non-admin merge into a protected target", func() {
		project := newProject("mr-protected-target")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)

		dev := newIdentity("mr-protected-dev")
		api.createPathRule(orgSlug, project, dev.userID, "", 1|2|4)
		devDir := cloneRepoAs(dev, workspace(), url, "dev-work")
		createFeature := runNipaAs(dev, devDir, "branch", "-c", "feature")
		Expect(createFeature.ExitCode).To(Equal(0), createFeature.Output())
		writeText(devDir, "feature.txt", "feature\n")
		Expect(runNipaAs(dev, devDir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(runNipaAs(dev, devDir, "push", "-m", uniqueMessage("dev feature")).ExitCode).To(Equal(0))
		Expect(runNipaAs(dev, devDir, "mr", "create", "--title", uniqueMessage("Protected target")).ExitCode).To(Equal(0))

		api.setBranchProtection(orgSlug, project, "main", true, nil, nil)

		for _, args := range [][]string{
			{"mr", "merge", "1"},
			{"mr", "merge", "1", "--strategy", "merge"},
			{"mr", "merge", "1", "--strategy", "rebase"},
		} {
			denied := runNipaAs(dev, devDir, args...)
			Expect(denied.ExitCode).NotTo(Equal(0), denied.Output())
			Expect(denied.Output()).To(ContainSubstring("permission"))
		}

		adminDir := cloneRepo(workspace(), url, "admin-work")
		merged := runNipa(adminDir, "mr", "merge", "1", "--strategy", "merge")
		Expect(merged.ExitCode).To(Equal(0), merged.Output())

		fresh := cloneRepo(workspace(), url, "fresh")
		Expect(readText(fresh, "feature.txt")).To(Equal("feature\n"))
	})

	It("assigns users and searches the list", func() {
		project := newProject("mr-assign-search")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Findable feature")).ExitCode).To(Equal(0))

		assignee := newIdentity("mr-assignee")
		other := newIdentity("mr-other-user")
		api.setMergeRequestAssignees(orgSlug, project, 1, []string{assignee.userID})

		view := runNipa(dir, "mr", "view", "1")
		Expect(view.ExitCode).To(Equal(0), view.Output())
		Expect(view.Output()).To(ContainSubstring("assignees: mr-assignee"))

		byAssignee := runNipa(dir, "mr", "list", "--assignee", assignee.userID)
		Expect(byAssignee.ExitCode).To(Equal(0), byAssignee.Output())
		Expect(byAssignee.Output()).To(ContainSubstring("Findable feature"))

		byOther := runNipa(dir, "mr", "list", "--assignee", other.userID)
		Expect(byOther.ExitCode).To(Equal(0), byOther.Output())
		Expect(byOther.Output()).NotTo(ContainSubstring("Findable feature"))

		search := runNipa(dir, "mr", "list", "--search", "findable")
		Expect(search.ExitCode).To(Equal(0), search.Output())
		Expect(search.Output()).To(ContainSubstring("Findable feature"))

		noMatch := runNipa(dir, "mr", "list", "--search", "zzz-nothing")
		Expect(noMatch.ExitCode).To(Equal(0), noMatch.Output())
		Expect(noMatch.Output()).To(ContainSubstring("no merge requests"))
	})

	It("auto-drafts WIP titles and marks them ready when the prefix is removed", func() {
		project := newProject("mr-wip")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "WIP: half done").ExitCode).To(Equal(0))

		var view mergeRequestViewJSON
		runJSONInto(dir, &view, "mr", "view", "1", "--json")
		Expect(view.MergeRequest.Draft).To(BeTrue())

		blocked := runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))

		Expect(runNipa(dir, "mr", "update", "1", "--title", "All done").ExitCode).To(Equal(0))
		view = mergeRequestViewJSON{}
		runJSONInto(dir, &view, "mr", "view", "1", "--json")
		Expect(view.MergeRequest.Draft).To(BeFalse())

		merged := runNipa(dir, "mr", "merge", "1")
		Expect(merged.ExitCode).To(Equal(0), merged.Output())
	})

	It("requires named reviewers to approve before merging", func() {
		project := newProject("mr-required-reviewers")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Required reviewers")).ExitCode).To(Equal(0))

		requiredReviewer := newIdentity("mr-required-reviewer").promote()
		other := newIdentity("mr-other-reviewer").promote()
		requiredAPI := newAPIClient(cli.apiURL, requiredReviewer.email, requiredReviewer.password)
		otherAPI := newAPIClient(cli.apiURL, other.email, other.password)

		protection := api.setBranchProtectionFull(orgSlug, project, "main", branchProtectionOptions{
			Protected:         true,
			RequiredReviewers: []string{requiredReviewer.userID},
		})
		Expect(protection.RequiredReviewers).To(HaveLen(1))
		Expect(protection.RequiredReviewers[0].UserID).To(Equal(requiredReviewer.userID))

		otherAPI.submitMergeRequestReview(orgSlug, project, 1, "approved", "looks fine")
		check := api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("required_reviewers"))
		view := runNipa(dir, "mr", "view", "1")
		Expect(view.Output()).To(ContainSubstring("blocked by required_reviewers"))
		blocked := runNipa(dir, "mr", "merge", "1")
		Expect(blocked.ExitCode).NotTo(Equal(0))
		Expect(blocked.Output()).To(ContainSubstring("required reviewers"))

		requiredAPI.submitMergeRequestReview(orgSlug, project, 1, "approved", "approved")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(BeEmpty())

		merged := runNipa(dir, "mr", "merge", "1")
		Expect(merged.ExitCode).To(Equal(0), merged.Output())
	})

	It("gates merging on required status checks", func() {
		project := newProject("mr-status-checks")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Checked work")).ExitCode).To(Equal(0))

		required := true
		protection := api.setBranchProtectionFull(orgSlug, project, "main", branchProtectionOptions{
			Protected:           true,
			RequireStatusChecks: &required,
			RequiredChecks:      []string{"build"},
		})
		Expect(protection.RequireStatusChecks).To(BeTrue())
		Expect(protection.RequiredChecks).To(Equal([]string{"build"}))

		check := api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("status_checks"))
		view := runNipa(dir, "mr", "view", "1")
		Expect(view.Output()).To(ContainSubstring("blocked by status_checks"))

		api.reportMergeRequestCheck(orgSlug, project, 1, "build", "failed", "")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("status_checks"))

		api.reportMergeRequestCheck(orgSlug, project, 1, "build", "success", "https://ci.example/run/1")
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(BeEmpty())

		checks := runNipa(dir, "mr", "checks", "1")
		Expect(checks.ExitCode).To(Equal(0), checks.Output())
		Expect(checks.Output()).To(ContainSubstring("build"))
		Expect(checks.Output()).To(ContainSubstring("success"))

		// a new push starts a clean slate for the new head
		writeText(dir, "more.txt", "more\n")
		Expect(runNipa(dir, "add", "more.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "more work").ExitCode).To(Equal(0))
		check = api.checkMergeRequest(orgSlug, project, 1)
		Expect(check.BlockedBy).To(Equal("status_checks"))

		reported := runNipa(dir, "mr", "check", "1", "--name", "build", "--state", "success")
		Expect(reported.ExitCode).To(Equal(0), reported.Output())

		merged := runNipa(dir, "mr", "merge", "1")
		Expect(merged.ExitCode).To(Equal(0), merged.Output())
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

		badStrategy := runNipa(dir, "mr", "merge", "1", "--strategy", "octopus")
		Expect(badStrategy.ExitCode).To(Equal(1))
		Expect(badStrategy.Output()).To(ContainSubstring("strategy must be one of"))
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

		// update toggles the draft state in both directions
		readyViaUpdate := runNipa(dir, "mr", "update", "1", "--draft=false")
		Expect(readyViaUpdate.ExitCode).To(Equal(0), readyViaUpdate.Output())
		draftedAgain := runNipa(dir, "mr", "update", "1", "--draft")
		Expect(draftedAgain.ExitCode).To(Equal(0), draftedAgain.Output())
		blockedAgain := runNipa(dir, "mr", "merge", "1")
		Expect(blockedAgain.ExitCode).NotTo(Equal(0))
		Expect(blockedAgain.Output()).To(ContainSubstring("draft"))

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
		Expect(protection.DismissStaleApprovals).To(BeTrue())

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
