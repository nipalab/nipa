package suite_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa mcp", func() {
	It("initializes and lists the read-only tools", func() {
		project := newProject("mcp-init")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		m := startMCP(dir, false)
		Expect(m.info.Name).To(Equal("nipa"))
		Expect(m.info.Version).NotTo(BeEmpty())

		names := m.toolNames()
		Expect(names).To(ConsistOf(
			"nipa_status",
			"nipa_diff",
			"nipa_log",
			"nipa_branch_list",
			"nipa_mr_list",
			"nipa_lock_list",
			"nipa_lock_query",
			"nipa_tag_list",
			"nipa_conflicts",
			"nipa_mr_view",
			"nipa_mr_diff",
			"nipa_mr_threads",
			"nipa_mr_commits",
			"nipa_mr_timeline",
			"nipa_asset_diff",
			"nipa_describe_changes",
			"nipa_asset_check",
			"nipa_mr_describe_changes",
			"nipa_meta_warnings",
		))
	})

	It("registers the mutating tools with --allow-write", func() {
		project := newProject("mcp-write-tools")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		m := startMCP(dir, true)
		names := m.toolNames()
		Expect(names).To(ConsistOf(
			"nipa_status",
			"nipa_diff",
			"nipa_log",
			"nipa_branch_list",
			"nipa_mr_list",
			"nipa_lock_list",
			"nipa_lock_query",
			"nipa_tag_list",
			"nipa_conflicts",
			"nipa_mr_view",
			"nipa_mr_diff",
			"nipa_mr_threads",
			"nipa_mr_commits",
			"nipa_mr_timeline",
			"nipa_asset_diff",
			"nipa_describe_changes",
			"nipa_asset_check",
			"nipa_mr_describe_changes",
			"nipa_meta_warnings",
			"nipa_add",
			"nipa_restore",
			"nipa_push",
			"nipa_update",
			"nipa_switch",
			"nipa_branch_create",
			"nipa_tag_create",
			"nipa_lock",
			"nipa_unlock",
			"nipa_merge",
			"nipa_resolve_conflict",
			"nipa_godot_merge",
			"nipa_mr_create",
			"nipa_mr_update",
			"nipa_mr_comment",
			"nipa_mr_reply",
			"nipa_mr_resolve",
			"nipa_mr_review",
			"nipa_mr_request_review",
			"nipa_mr_check",
			"nipa_mr_merge",
		))
	})

	It("scopes write tools with --allow and answers tag and lock queries", func() {
		project := newProject("mcp-allow")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))

		m := startMCPWithArgs(dir, cli.tokenFile, "--allow", "branch,lock")
		names := m.toolNames()
		Expect(names).To(ContainElements("nipa_tag_create", "nipa_lock", "nipa_unlock", "nipa_branch_create"))
		Expect(names).NotTo(ContainElements("nipa_push", "nipa_add", "nipa_merge", "nipa_mr_create"))

		tagResult := m.callTool("nipa_tag_list", map[string]any{"repo": dir})
		Expect(tagResult.IsError).To(BeFalse(), tagResult.text())
		var tags struct {
			Tags []struct {
				Name string `json:"name"`
			} `json:"tags"`
		}
		tagResult.decode(&tags)
		Expect(tags.Tags).To(HaveLen(1))
		Expect(tags.Tags[0].Name).To(Equal("v1.0.0"))

		lockResult := m.callTool("nipa_lock", map[string]any{"repo": dir, "path": "assets"})
		Expect(lockResult.IsError).To(BeFalse(), lockResult.text())
		queryResult := m.callTool("nipa_lock_query", map[string]any{"repo": dir, "path": "assets/hero.png"})
		Expect(queryResult.IsError).To(BeFalse(), queryResult.text())
		var query struct {
			Locked bool `json:"locked"`
			Locks  []struct {
				Path string `json:"path"`
			} `json:"locks"`
		}
		queryResult.decode(&query)
		Expect(query.Locked).To(BeTrue())
		Expect(query.Locks[0].Path).To(Equal("assets"))
	})

	It("describes asset changes with image thumbnails", func() {
		project := newProject("mcp-asset-diff")
		seedRepo(orgSlug, project, map[string][]byte{"tex.png": testPNG(4)}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeBytes(dir, "tex.png", testPNG(8))

		m := startMCP(dir, false)

		diffResult := m.callTool("nipa_asset_diff", map[string]any{"repo": dir, "path": "tex.png", "include_images": true})
		Expect(diffResult.IsError).To(BeFalse(), diffResult.text())
		var asset struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
			Image  struct {
				Old struct {
					Width int `json:"width"`
				} `json:"old"`
				New struct {
					Width int `json:"width"`
				} `json:"new"`
			} `json:"image"`
		}
		diffResult.decode(&asset)
		Expect(asset.Kind).To(Equal("image"))
		Expect(asset.Status).To(Equal("modified"))
		Expect(asset.Image.Old.Width).To(Equal(4))
		Expect(asset.Image.New.Width).To(Equal(8))
		images := 0
		for _, c := range diffResult.Content {
			if c.Type == "image" {
				Expect(c.MimeType).To(Equal("image/png"))
				images++
			}
		}
		Expect(images).To(Equal(2))

		describeResult := m.callTool("nipa_describe_changes", map[string]any{"repo": dir})
		Expect(describeResult.IsError).To(BeFalse(), describeResult.text())
		var summary struct {
			Files []struct {
				Path     string `json:"path"`
				Headline string `json:"headline"`
			} `json:"files"`
		}
		describeResult.decode(&summary)
		Expect(summary.Files).To(HaveLen(1))
		Expect(summary.Files[0].Path).To(Equal("tex.png"))
		Expect(summary.Files[0].Headline).NotTo(BeEmpty())
	})

	It("rejects unknown capabilities", func() {
		res := runNipa(workspace(), "mcp", "--allow", "admin")
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("unknown capability"))
	})

	It("returns status and log", func() {
		project := newProject("mcp-status-log")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "b.txt", "b\n")

		m := startMCP(dir, false)

		statusResult := m.callTool("nipa_status", map[string]any{"repo": dir})
		Expect(statusResult.IsError).To(BeFalse(), statusResult.text())
		var status statusJSON
		statusResult.decode(&status)
		Expect(status.Branch).To(Equal("main"))
		Expect(status.Untracked).To(ContainElement("b.txt"))

		logResult := m.callTool("nipa_log", map[string]any{"repo": dir, "limit": 10})
		Expect(logResult.IsError).To(BeFalse(), logResult.text())
		var log logJSON
		logResult.decode(&log)
		Expect(log.Commits).To(HaveLen(1))
		Expect(log.Commits[0].Message).To(ContainSubstring("first commit"))
	})

	It("returns branch lists, diffs and lock lists", func() {
		project := newProject("mcp-read")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "a.txt", "v2\n")

		m := startMCP(dir, false)

		branchResult := m.callTool("nipa_branch_list", map[string]any{"repo": dir})
		Expect(branchResult.IsError).To(BeFalse(), branchResult.text())
		var branches branchesJSON
		branchResult.decode(&branches)
		Expect(branches.Current).To(Equal("main"))
		Expect(branches.Branches).To(HaveLen(1))

		diffResult := m.callTool("nipa_diff", map[string]any{"repo": dir})
		Expect(diffResult.IsError).To(BeFalse(), diffResult.text())
		var diffOut diffJSON
		diffResult.decode(&diffOut)
		Expect(diffOut.Changes).To(HaveLen(1))
		Expect(diffOut.Changes[0].Path).To(Equal("a.txt"))
		Expect(diffOut.Changes[0].Hunks).NotTo(BeEmpty())

		lockResult := m.callTool("nipa_lock_list", map[string]any{"repo": dir})
		Expect(lockResult.IsError).To(BeFalse(), lockResult.text())
		var locks locksJSON
		lockResult.decode(&locks)
		Expect(locks.Locks).To(BeEmpty())

		mrResult := m.callTool("nipa_mr_list", map[string]any{"repo": dir})
		Expect(mrResult.IsError).To(BeFalse(), mrResult.text())
		var mrs mergeRequestsJSON
		mrResult.decode(&mrs)
		Expect(mrs.MergeRequests).To(BeEmpty())
	})

	It("stages and pushes through tools", func() {
		project := newProject("mcp-push")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "b.txt", "from mcp\n")

		m := startMCP(dir, true)

		addResult := m.callTool("nipa_add", map[string]any{"repo": dir, "paths": []string{"b.txt"}})
		Expect(addResult.IsError).To(BeFalse(), addResult.text())
		var staged statusJSON
		addResult.decode(&staged)
		Expect(staged.Staged).To(ContainElement("b.txt"))

		pushResult := m.callTool("nipa_push", map[string]any{"repo": dir, "message": uniqueMessage("mcp push")})
		Expect(pushResult.IsError).To(BeFalse(), pushResult.text())
		var pushed struct {
			Branch   string `json:"branch"`
			CommitID string `json:"commit_id"`
		}
		pushResult.decode(&pushed)
		Expect(pushed.Branch).To(Equal("main"))
		Expect(pushed.CommitID).NotTo(BeEmpty())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "b.txt")).To(Equal("from mcp\n"))
		var log logJSON
		runJSONInto(fresh, &log, "log", "--json")
		Expect(log.Commits[0].Message).To(HaveSuffix("Nipa-Agent: nipa-e2e/0.0.1"))
	})

	It("creates branches, locks and merge requests through tools", func() {
		project := newProject("mcp-write")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		m := startMCP(dir, true)

		branchResult := m.callTool("nipa_branch_create", map[string]any{"repo": dir, "name": "feature"})
		Expect(branchResult.IsError).To(BeFalse(), branchResult.text())
		var created struct {
			Name     string `json:"name"`
			CommitID string `json:"commit_id"`
		}
		branchResult.decode(&created)
		Expect(created.Name).To(Equal("feature"))

		lockResult := m.callTool("nipa_lock", map[string]any{"repo": dir, "path": "assets/hero.png"})
		Expect(lockResult.IsError).To(BeFalse(), lockResult.text())
		var lock struct {
			Path  string `json:"path"`
			Scope string `json:"scope"`
		}
		lockResult.decode(&lock)
		Expect(lock.Path).To(Equal("assets/hero.png"))
		Expect(lock.Scope).To(Equal("branch"))

		listResult := m.callTool("nipa_lock_list", map[string]any{"repo": dir})
		var locks locksJSON
		listResult.decode(&locks)
		Expect(locks.Locks).To(HaveLen(1))

		unlockResult := m.callTool("nipa_unlock", map[string]any{"repo": dir, "path": "assets/hero.png"})
		Expect(unlockResult.IsError).To(BeFalse(), unlockResult.text())

		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))

		mrResult := m.callTool("nipa_mr_create", map[string]any{"repo": dir, "title": uniqueMessage("MCP MR")})
		Expect(mrResult.IsError).To(BeFalse(), mrResult.text())
		var mr struct {
			Number       int64  `json:"number"`
			SourceBranch string `json:"source_branch"`
			Status       string `json:"status"`
		}
		mrResult.decode(&mr)
		Expect(mr.Number).To(Equal(int64(1)))
		Expect(mr.SourceBranch).To(Equal("feature"))
		Expect(mr.Status).To(Equal("open"))

		cli := runNipa(dir, "mr", "list", "--json")
		Expect(cli.ExitCode).To(Equal(0), cli.Output())
		Expect(cli.Stdout).To(ContainSubstring("feature"))
	})

	It("diffs two revisions and staged changes", func() {
		project := newProject("mcp-diff-rev")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "first commit")
		seedRepo(orgSlug, project, map[string][]byte{"b.txt": []byte("b\n")}, "second commit")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(2))
		first, second := log.Commits[1].ID, log.Commits[0].ID

		writeText(dir, "a.txt", "a changed\n")
		writeText(dir, "c.txt", "c\n")
		Expect(runNipa(dir, "add", "c.txt").ExitCode).To(Equal(0))

		m := startMCP(dir, false)
		paths := func(out diffJSON) map[string]string {
			got := map[string]string{}
			for _, change := range out.Changes {
				got[change.Path] = change.Status
			}
			return got
		}

		revResult := m.callTool("nipa_diff", map[string]any{"repo": dir, "revisions": []string{first, second}})
		Expect(revResult.IsError).To(BeFalse(), revResult.text())
		var revDiff diffJSON
		revResult.decode(&revDiff)
		Expect(paths(revDiff)).To(Equal(map[string]string{"b.txt": "A"}))

		baseResult := m.callTool("nipa_diff", map[string]any{"repo": dir, "revisions": []string{second, first}, "paths": []string{"b.txt"}})
		Expect(baseResult.IsError).To(BeFalse(), baseResult.text())
		var reverse diffJSON
		baseResult.decode(&reverse)
		Expect(paths(reverse)).To(Equal(map[string]string{"b.txt": "D"}))

		stagedResult := m.callTool("nipa_diff", map[string]any{"repo": dir, "staged": true})
		Expect(stagedResult.IsError).To(BeFalse(), stagedResult.text())
		var staged diffJSON
		stagedResult.decode(&staged)
		Expect(paths(staged)).To(Equal(map[string]string{"c.txt": "A"}))

		workResult := m.callTool("nipa_diff", map[string]any{"repo": dir})
		Expect(workResult.IsError).To(BeFalse(), workResult.text())
		var work diffJSON
		workResult.decode(&work)
		Expect(paths(work)).To(HaveKey("a.txt"))

		mixed := m.callTool("nipa_diff", map[string]any{"repo": dir, "revisions": []string{first}, "staged": true})
		Expect(mixed.IsError).To(BeTrue())
		Expect(mixed.text()).To(ContainSubstring("staged cannot be combined with revisions"))
	})

	It("filters merge requests by status", func() {
		project := newProject("mcp-mr-filter")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		for _, name := range []string{"feature-a", "feature-b", "feature-c"} {
			Expect(runNipa(dir, "branch", "-c", name).ExitCode).To(Equal(0))
			writeText(dir, name+".txt", name+"\n")
			Expect(runNipa(dir, "add", name+".txt").ExitCode).To(Equal(0))
			Expect(pushRepo(dir, name+" work").ExitCode).To(Equal(0))
			Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("MR "+name)).ExitCode).To(Equal(0))
			Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		}
		Expect(runNipa(dir, "mr", "merge", "1").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "close", "2").ExitCode).To(Equal(0))

		m := startMCP(dir, false)
		sources := func(args map[string]any) []string {
			result := m.callTool("nipa_mr_list", args)
			Expect(result.IsError).To(BeFalse(), result.text())
			var out mergeRequestsJSON
			result.decode(&out)
			names := []string{}
			for _, mr := range out.MergeRequests {
				names = append(names, mr.SourceBranch)
			}
			return names
		}

		Expect(sources(map[string]any{"repo": dir})).To(ConsistOf("feature-a", "feature-b", "feature-c"))
		Expect(sources(map[string]any{"repo": dir, "status": "open"})).To(ConsistOf("feature-c"))
		Expect(sources(map[string]any{"repo": dir, "status": "merged"})).To(ConsistOf("feature-a"))
		Expect(sources(map[string]any{"repo": dir, "status": "closed"})).To(ConsistOf("feature-b"))
		Expect(sources(map[string]any{"repo": dir, "limit": 1})).To(HaveLen(1))
	})

	It("surfaces the server permission error for a read-only user", func() {
		project := newProject("mcp-read-only")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		reader := newIdentity("mcp-reader")
		api.createPathRule(orgSlug, project, reader.userID, "", 1)
		dir := cloneRepoAs(reader, workspace(), url, "work")
		writeText(dir, "b.txt", "from reader\n")

		m := startMCPWithTokenFile(dir, true, reader.tokenFile)
		Expect(m.toolNames()).To(ContainElement("nipa_push"))

		addResult := m.callTool("nipa_add", map[string]any{"repo": dir, "paths": []string{"b.txt"}})
		Expect(addResult.IsError).To(BeFalse(), addResult.text())

		for _, call := range []struct {
			tool string
			args map[string]any
		}{
			{"nipa_push", map[string]any{"repo": dir, "message": uniqueMessage("denied push")}},
			{"nipa_branch_create", map[string]any{"repo": dir, "name": "denied"}},
			{"nipa_lock", map[string]any{"repo": dir, "path": "a.txt"}},
		} {
			result := m.callTool(call.tool, call.args)
			Expect(result.IsError).To(BeTrue(), "%s: %s", call.tool, result.text())
			Expect(result.text()).To(ContainSubstring("no permission"), call.tool)
		}

		adminDir := cloneRepo(workspace(), url, "admin")
		Expect(fileExists(adminDir, "b.txt")).To(BeFalse())
		var log logJSON
		runJSONInto(adminDir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(1))
		var branches branchesJSON
		runJSONInto(adminDir, &branches, "branch", "-a", "--json")
		Expect(branches.Branches).To(HaveLen(1))
		var locks locksJSON
		runJSONInto(adminDir, &locks, "lock", "list", "--json")
		Expect(locks.Locks).To(BeEmpty())
	})

	It("reports tool errors with hints", func() {
		project := newProject("mcp-errors")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		m := startMCP(dir, false)

		result := m.callTool("nipa_status", map[string]any{"repo": filepath.Join(parent, "not-a-repo")})
		Expect(result.IsError).To(BeTrue())
		Expect(result.text()).NotTo(BeEmpty())
	})

	It("fails online tools without a stored token", func() {
		project := newProject("mcp-no-token")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		emptyTokenFile := filepath.Join(GinkgoT().TempDir(), "empty-tokens.json")
		writeText(filepath.Dir(emptyTokenFile), filepath.Base(emptyTokenFile), "{}\n")

		m := startMCPWithTokenFile(dir, false, emptyTokenFile)
		result := m.callTool("nipa_log", map[string]any{"repo": dir})
		Expect(result.IsError).To(BeTrue())
		Expect(result.text()).To(ContainSubstring("login"))
	})
})
