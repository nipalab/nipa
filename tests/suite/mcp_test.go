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
			"nipa_add",
			"nipa_push",
			"nipa_branch_create",
			"nipa_lock",
			"nipa_unlock",
			"nipa_mr_create",
		))
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
