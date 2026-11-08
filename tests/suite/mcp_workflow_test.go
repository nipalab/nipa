package suite_test

import (
	"encoding/json"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const mcpAgentTrailer = "Nipa-Agent: nipa-e2e/0.0.1"

type mcpThreadJSON struct {
	ID       string `json:"id"`
	FilePath string `json:"file_path"`
	NewLine  *int64 `json:"new_line"`
	Resolved bool   `json:"resolved"`
	Comments []struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	} `json:"comments"`
}

type mcpLockQueryJSON struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Scope  string `json:"scope"`
	Locked bool   `json:"locked"`
	Locks  []struct {
		Path   string `json:"path"`
		Scope  string `json:"scope"`
		Branch string `json:"branch"`
	} `json:"locks"`
	OtherScopes []struct {
		Path   string `json:"path"`
		Scope  string `json:"scope"`
		Branch string `json:"branch"`
	} `json:"other_scopes"`
}

// callToolOK calls a tool, fails on a tool error and decodes the structured
// result into out.
func (m *mcpProc) callToolOK(name string, args map[string]any, out any) mcpToolResult {
	GinkgoHelper()
	result := m.callTool(name, args)
	Expect(result.IsError).To(BeFalse(), "%s: %s", name, result.text())
	if out != nil {
		result.decode(out)
	}
	return result
}

// callToolErr returns the error text of a call that must fail, either as a
// JSON-RPC error (unknown tool) or as a tool error result.
func (m *mcpProc) callToolErr(name string, args map[string]any) string {
	GinkgoHelper()
	resp := m.request("tools/call", map[string]any{"name": name, "arguments": args})
	if resp.Error != nil {
		return resp.Error.Message
	}
	var result mcpToolResult
	Expect(json.Unmarshal(resp.Result, &result)).To(Succeed())
	Expect(result.IsError).To(BeTrue(), "%s unexpectedly succeeded: %s", name, result.text())
	return result.text()
}

func numberedLines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(prefix)
		b.WriteString(strings.Repeat("x", 40))
		b.WriteString(" ")
		b.WriteString(strings.Repeat("y", i%7))
		b.WriteString("\n")
	}
	return b.String()
}

const godotBaseScene = "[gd_scene load_steps=2 format=3]\n\n[ext_resource type=\"Script\" path=\"res://level.gd\" id=\"1_lvl\"]\n\n[node name=\"Level\" type=\"Node2D\"]\nscript = ExtResource(\"1_lvl\")\n\n[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n\n[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n\n[node name=\"Camera\" type=\"Camera2D\" parent=\".\"]\n"

func moveNode(scene, node, position string) string {
	header := "[node name=\"" + node + "\" type=\"Sprite2D\" parent=\".\"]\n"
	return strings.Replace(scene, header+"position = Vector2(0, 0)", header+"position = Vector2("+position+")", 1)
}

// divergeGodotScene pushes featureScene on a feature branch and mainScene on
// main, leaving the working copy on main with a merge still to run.
func divergeGodotScene(featureScene, mainScene string) (string, string) {
	GinkgoHelper()
	project := newProject("mcp-godot")
	seedRepo(orgSlug, project, map[string][]byte{"level.tscn": []byte(godotBaseScene)}, "seed")
	url := repoURLFor(orgSlug, project)
	dir := cloneRepo(workspace(), url, "work")

	Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
	writeText(dir, "level.tscn", featureScene)
	Expect(runNipa(dir, "add", "level.tscn").ExitCode).To(Equal(0))
	Expect(pushRepo(dir, "feature edits level").ExitCode).To(Equal(0))

	Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
	writeText(dir, "level.tscn", mainScene)
	Expect(runNipa(dir, "add", "level.tscn").ExitCode).To(Equal(0))
	Expect(pushRepo(dir, "main edits level").ExitCode).To(Equal(0))
	return dir, url
}

const unityPrefabOld = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!1 &100
GameObject:
  m_Component:
  - component: {fileID: 101}
  m_Name: Foo
--- !u!114 &101
MonoBehaviour:
  m_GameObject: {fileID: 100}
  m_Enabled: 1
  speed: 1
  material: {fileID: 2100000, guid: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, type: 2}
`

func unityMaterial(name string) []byte {
	return []byte("%YAML 1.1\n%TAG !u! tag:unity3d.com,2011:\n--- !u!21 &2100000\nMaterial:\n  m_Name: " + name + "\n")
}

func unityMeta(guid string) []byte {
	return []byte("fileFormatVersion: 2\nguid: " + guid + "\n")
}

func unityProjectFiles() map[string][]byte {
	return map[string][]byte{
		"ProjectSettings/ProjectVersion.txt": []byte("m_EditorVersion: 2022.3.0f1\n"),
		"Assets/Foo.prefab":                  []byte(unityPrefabOld),
		"Assets/Foo.prefab.meta":             unityMeta("11111111111111111111111111111111"),
		"Assets/Hero.mat":                    unityMaterial("Hero"),
		"Assets/Hero.mat.meta":               unityMeta("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		"Assets/Other.mat":                   unityMaterial("Other"),
		"Assets/Other.mat.meta":              unityMeta("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
	}
}

func unityPrefabNew() string {
	s := strings.Replace(unityPrefabOld, "speed: 1", "speed: 2", 1)
	return strings.Replace(s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 1)
}

var _ = Describe("nipa mcp workflows", func() {
	It("drives a merge request review over MCP tools and prompts", func() {
		project := newProject("mcp-mr-flow")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaassets.yml": []byte(assetBudget),
			"base.txt":        []byte("base\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		author := startMCPWithArgs(dir, cli.tokenFile, "--allow", "mr,branch,stage,push")
		var branch struct {
			Name string `json:"name"`
		}
		author.callToolOK("nipa_branch_create", map[string]any{"repo": dir, "name": "feature"}, &branch)
		Expect(branch.Name).To(Equal("feature"))

		writeText(dir, "feature.txt", numberedLines("feature ", 30))
		writeText(dir, "notes.txt", numberedLines("note ", 30))
		writeBytes(dir, "art/odd.png", testPNG(6))
		author.callToolOK("nipa_add", map[string]any{"repo": dir, "paths": []string{"feature.txt", "notes.txt", "art/odd.png"}}, nil)
		author.callToolOK("nipa_push", map[string]any{"repo": dir, "message": uniqueMessage("feature work")}, nil)

		title := uniqueMessage("MCP workflow")
		var created struct {
			Number       int64  `json:"number"`
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
			Description  string `json:"description"`
		}
		author.callToolOK("nipa_mr_create", map[string]any{"repo": dir, "title": title, "description": "Adds the feature."}, &created)
		Expect(created.SourceBranch).To(Equal("feature"))
		Expect(created.TargetBranch).To(Equal("main"))
		Expect(created.Description).To(Equal("Adds the feature.\n\n" + mcpAgentTrailer))
		number := created.Number

		var full struct {
			Files []struct {
				Path      string   `json:"path"`
				Patch     []string `json:"patch"`
				Truncated bool     `json:"truncated"`
			} `json:"files"`
			OmittedFiles int `json:"omitted_files"`
		}
		author.callToolOK("nipa_mr_diff", map[string]any{"repo": dir, "number": number}, &full)
		Expect(full.Files).To(HaveLen(3))
		Expect(full.OmittedFiles).To(BeZero())
		for _, f := range full.Files {
			Expect(f.Truncated).To(BeFalse(), f.Path)
		}

		var capped struct {
			Files []struct {
				Path      string `json:"path"`
				Truncated bool   `json:"truncated"`
			} `json:"files"`
			OmittedFiles int `json:"omitted_files"`
		}
		author.callToolOK("nipa_mr_diff", map[string]any{"repo": dir, "number": number, "max_bytes": 200}, &capped)
		Expect(capped.OmittedFiles).To(BeNumerically(">=", 1))
		Expect(len(capped.Files) + capped.OmittedFiles).To(Equal(3))
		truncated := false
		for _, f := range capped.Files {
			truncated = truncated || f.Truncated
		}
		Expect(truncated).To(BeTrue(), "one text file is cut by the budget")

		var top mcpThreadJSON
		author.callToolOK("nipa_mr_comment", map[string]any{"repo": dir, "number": number, "body": "Top-level note"}, &top)
		Expect(top.FilePath).To(BeEmpty())
		Expect(top.Comments).To(HaveLen(1))
		Expect(top.Comments[0].Body).To(Equal("Top-level note\n\n" + mcpAgentTrailer))

		var inline mcpThreadJSON
		author.callToolOK("nipa_mr_comment", map[string]any{"repo": dir, "number": number, "body": "Inline note", "file": "feature.txt", "new_line": 1}, &inline)
		Expect(inline.FilePath).To(Equal("feature.txt"))
		Expect(inline.NewLine).NotTo(BeNil())
		Expect(*inline.NewLine).To(Equal(int64(1)))

		noFile := author.callToolErr("nipa_mr_comment", map[string]any{"repo": dir, "number": number, "body": "x", "new_line": 1})
		Expect(noFile).To(ContainSubstring("need a file"))

		var reply struct {
			ThreadID string `json:"thread_id"`
			Body     string `json:"body"`
		}
		author.callToolOK("nipa_mr_reply", map[string]any{"repo": dir, "number": number, "thread_id": top.ID, "body": "Follow-up"}, &reply)
		Expect(reply.ThreadID).To(Equal(top.ID))
		Expect(reply.Body).To(HaveSuffix(mcpAgentTrailer))

		var resolved mcpThreadJSON
		author.callToolOK("nipa_mr_resolve", map[string]any{"repo": dir, "number": number, "thread_id": top.ID}, &resolved)
		Expect(resolved.Resolved).To(BeTrue())

		var threads struct {
			Threads []mcpThreadJSON `json:"threads"`
		}
		author.callToolOK("nipa_mr_threads", map[string]any{"repo": dir, "number": number}, &threads)
		Expect(threads.Threads).To(HaveLen(2))
		for _, t := range threads.Threads {
			if t.ID == top.ID {
				Expect(t.Resolved).To(BeTrue())
				Expect(t.Comments).To(HaveLen(2))
			} else {
				Expect(t.FilePath).To(Equal("feature.txt"))
				Expect(t.Resolved).To(BeFalse())
			}
		}

		own := author.callToolErr("nipa_mr_review", map[string]any{"repo": dir, "number": number, "state": "approved", "body": "LGTM"})
		Expect(own).To(ContainSubstring("cannot review your own merge request"))

		reviewer := newIdentity("mcp-reviewer").promote()
		reviewerMCP := startMCPWithArgs(dir, reviewer.tokenFile, "--allow", "mr")
		var review struct {
			State string `json:"state"`
			Body  string `json:"body"`
		}
		reviewerMCP.callToolOK("nipa_mr_review", map[string]any{"repo": dir, "number": number, "state": "changes_requested", "body": "Please split notes.txt"}, &review)
		Expect(review.State).To(Equal("changes_requested"))
		Expect(review.Body).To(Equal("Please split notes.txt\n\n" + mcpAgentTrailer))

		var check struct {
			Name  string `json:"name"`
			State string `json:"state"`
		}
		author.callToolOK("nipa_mr_check", map[string]any{"repo": dir, "number": number, "name": "e2e/lint", "state": "success", "details_url": "https://ci.example.com/1"}, &check)
		Expect(check.Name).To(Equal("e2e/lint"))
		Expect(check.State).To(Equal("success"))

		var view struct {
			MergeRequest struct {
				Number      int64  `json:"number"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Status      string `json:"status"`
			} `json:"merge_request"`
			Mergeability *struct {
				Status    string `json:"status"`
				BlockedBy string `json:"blocked_by"`
			} `json:"mergeability"`
			Reviews []struct {
				State string `json:"state"`
			} `json:"reviews"`
			Checks []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"checks"`
		}
		reviewerMCP.callToolOK("nipa_mr_view", map[string]any{"repo": dir, "number": number}, &view)
		Expect(view.MergeRequest.Title).To(Equal(title))
		Expect(view.MergeRequest.Status).To(Equal("open"))
		Expect(view.MergeRequest.Description).To(HaveSuffix(mcpAgentTrailer))
		Expect(view.Mergeability).NotTo(BeNil())
		Expect(view.Reviews).To(HaveLen(1))
		Expect(view.Reviews[0].State).To(Equal("changes_requested"))
		Expect(view.Checks).To(HaveLen(1))
		Expect(view.Checks[0].Name).To(Equal("e2e/lint"))

		var timeline struct {
			Timeline []struct {
				Kind string `json:"kind"`
			} `json:"timeline"`
		}
		author.callToolOK("nipa_mr_timeline", map[string]any{"repo": dir, "number": number}, &timeline)
		kinds := []string{}
		for _, item := range timeline.Timeline {
			kinds = append(kinds, item.Kind)
		}
		Expect(kinds).To(ContainElements("opened", "review_submitted"))

		var commits logJSON
		author.callToolOK("nipa_mr_commits", map[string]any{"repo": dir, "number": number}, &commits)
		Expect(commits.Commits).To(HaveLen(1))
		Expect(commits.Commits[0].Message).To(ContainSubstring("feature work"))
		Expect(commits.Commits[0].Message).To(HaveSuffix(mcpAgentTrailer))

		prompt, rpcErr := reviewerMCP.getPrompt("nipa_review_mr", map[string]string{"repo": dir, "number": strconv.FormatInt(number, 10)})
		Expect(rpcErr).To(BeNil())
		Expect(prompt).To(ContainSubstring("title: " + title))
		Expect(prompt).To(ContainSubstring("<diff>"))
		Expect(prompt).To(ContainSubstring("+feature "))
		Expect(prompt).To(ContainSubstring("<threads>"))
		Expect(prompt).To(ContainSubstring("<budget_violations>"))
		Expect(prompt).To(ContainSubstring("art/odd.png: power_of_two"))

		_, rpcErr = reviewerMCP.getPrompt("nipa_review_mr", map[string]string{"repo": dir, "number": "zero"})
		Expect(rpcErr).NotTo(BeNil())
		Expect(rpcErr.Error.Message).To(ContainSubstring("positive merge request number"))
	})

	It("registers only the tools of the allowed capabilities", func() {
		project := newProject("mcp-allow-lock")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "b.txt", "b\n")
		Expect(runNipa(dir, "add", "b.txt").ExitCode).To(Equal(0))

		m := startMCPWithArgs(dir, cli.tokenFile, "--allow", "lock")
		names := m.toolNames()
		Expect(names).To(ContainElements("nipa_lock", "nipa_unlock", "nipa_status", "nipa_mr_view"))
		for _, absent := range []string{"nipa_push", "nipa_add", "nipa_mr_create", "nipa_mr_comment", "nipa_merge", "nipa_godot_merge", "nipa_mr_merge", "nipa_branch_create", "nipa_update"} {
			Expect(names).NotTo(ContainElement(absent))
		}

		msg := m.callToolErr("nipa_push", map[string]any{"repo": dir, "message": uniqueMessage("blocked")})
		Expect(msg).To(ContainSubstring(`unknown tool "nipa_push"`))
		msg = m.callToolErr("nipa_mr_create", map[string]any{"repo": dir, "title": "blocked"})
		Expect(msg).To(ContainSubstring(`unknown tool "nipa_mr_create"`))

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(1))
		var mrs mergeRequestsJSON
		runJSONInto(dir, &mrs, "mr", "list", "--json")
		Expect(mrs.MergeRequests).To(BeEmpty())
	})

	It("describes Unity scene changes with resolved GUIDs and lints .meta files", func() {
		project := newProject("mcp-unity")
		seedRepo(orgSlug, project, unityProjectFiles(), "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		writeText(dir, "Assets/Foo.prefab", unityPrefabNew())

		m := startMCP(dir, false)
		var asset struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
			Scene  struct {
				Engine  string `json:"engine"`
				Changed int    `json:"changed"`
				Changes []struct {
					Status string `json:"status"`
					Props  []struct {
						Key string `json:"key"`
						Old string `json:"old"`
						New string `json:"new"`
					} `json:"props"`
				} `json:"changes"`
				Refs []struct {
					Path   string `json:"path"`
					Status string `json:"status"`
				} `json:"refs"`
			} `json:"scene"`
		}
		result := m.callToolOK("nipa_asset_diff", map[string]any{"repo": dir, "path": "Assets/Foo.prefab"}, &asset)
		Expect(asset.Kind).To(Equal("scene"))
		Expect(asset.Status).To(Equal("modified"))
		Expect(asset.Scene.Engine).To(Equal("unity"))
		Expect(asset.Scene.Changed).To(BeNumerically(">=", 1))
		props := map[string][2]string{}
		for _, c := range asset.Scene.Changes {
			for _, p := range c.Props {
				props[p.Key] = [2]string{p.Old, p.New}
			}
		}
		Expect(props).To(HaveKey(ContainSubstring("speed")), string(result.StructuredContent))
		refs := map[string]string{}
		for _, r := range asset.Scene.Refs {
			refs[r.Path] = r.Status
		}
		Expect(refs).To(HaveKeyWithValue("Assets/Other.mat", "added"), string(result.StructuredContent))
		Expect(refs).To(HaveKeyWithValue("Assets/Hero.mat", "removed"))
		Expect(string(result.StructuredContent)).NotTo(ContainSubstring("guid:bbbb"))

		writeBytes(dir, "Assets/New.png", testPNG(4))
		writeBytes(dir, "Assets/Gone.png.meta", unityMeta("cccccccccccccccccccccccccccccccc"))
		var warnings struct {
			Engine   string `json:"engine"`
			Warnings []struct {
				Path    string `json:"path"`
				Kind    string `json:"kind"`
				Related string `json:"related"`
			} `json:"warnings"`
		}
		m.callToolOK("nipa_meta_warnings", map[string]any{"repo": dir}, &warnings)
		Expect(warnings.Engine).To(Equal("unity"))
		kinds := map[string]string{}
		for _, w := range warnings.Warnings {
			kinds[w.Path] = w.Kind
		}
		Expect(kinds).To(HaveKeyWithValue("Assets/New.png", "missing-meta"))
		Expect(kinds).To(HaveKeyWithValue("Assets/Gone.png.meta", "orphan-meta"))
		Expect(kinds).NotTo(HaveKey("Assets/Foo.prefab"))

		bad := m.callToolErr("nipa_meta_warnings", map[string]any{"repo": dir, "engine": "unreal"})
		Expect(bad).To(ContainSubstring("unknown engine"))
	})

	It("summarizes a merge request touching a texture and a prefab", func() {
		project := newProject("mcp-mr-assets")
		files := unityProjectFiles()
		files["Assets/tex.png"] = testPNG(4)
		files["Assets/tex.png.meta"] = unityMeta("dddddddddddddddddddddddddddddddd")
		seedRepo(orgSlug, project, files, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		Expect(runNipa(dir, "branch", "-c", "art").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "lock", "Assets/tex.png").ExitCode).To(Equal(0))
		writeBytes(dir, "Assets/tex.png", testPNG(8))
		writeText(dir, "Assets/Foo.prefab", unityPrefabNew())
		Expect(runNipa(dir, "add", "Assets/tex.png", "Assets/Foo.prefab").ExitCode).To(Equal(0))
		pushed := pushRepo(dir, "retexture foo")
		Expect(pushed.ExitCode).To(Equal(0), pushed.Output())
		Expect(runNipa(dir, "mr", "create", "--title", uniqueMessage("Art pass")).ExitCode).To(Equal(0))

		m := startMCP(dir, false)
		var out struct {
			Number int64 `json:"number"`
			Base   struct {
				Kind     string `json:"kind"`
				Revision string `json:"revision"`
			} `json:"base"`
			Head struct {
				Revision string `json:"revision"`
			} `json:"head"`
			Files []struct {
				Path     string `json:"path"`
				Kind     string `json:"kind"`
				Headline string `json:"headline"`
			} `json:"files"`
			Omitted int `json:"omitted"`
		}
		result := m.callToolOK("nipa_mr_describe_changes", map[string]any{"repo": dir, "number": 1}, &out)
		Expect(out.Number).To(Equal(int64(1)))
		Expect(out.Base.Revision).NotTo(BeEmpty())
		Expect(out.Head.Revision).NotTo(BeEmpty())
		headlines := map[string]string{}
		for _, f := range out.Files {
			headlines[f.Path] = f.Kind + ": " + f.Headline
		}
		Expect(headlines).To(HaveLen(2), string(result.StructuredContent))
		Expect(headlines["Assets/tex.png"]).To(ContainSubstring("4×4 → 8×8"))
		Expect(headlines["Assets/Foo.prefab"]).To(HavePrefix("scene: "))
		Expect(headlines["Assets/Foo.prefab"]).To(ContainSubstring("changed"))
	})

	It("merges a conflicted Godot scene structurally through nipa_godot_merge", func() {
		feature := moveNode(godotBaseScene, "A", "16, 0") + "\n[node name=\"Coin\" type=\"Area2D\" parent=\".\"]\n"
		main := moveNode(godotBaseScene, "B", "0, 32") + "\n[node name=\"Door\" type=\"Area2D\" parent=\".\"]\n"
		dir, url := divergeGodotScene(feature, main)

		m := startMCPWithArgs(dir, cli.tokenFile, "--allow", "merge")
		var merged struct {
			Conflicts []string `json:"conflicts"`
			Next      string   `json:"next"`
		}
		m.callToolOK("nipa_merge", map[string]any{"repo": dir, "source": "feature"}, &merged)
		Expect(merged.Conflicts).To(ConsistOf("level.tscn"))
		Expect(merged.Next).To(ContainSubstring("nipa_resolve_conflict"))
		Expect(readText(dir, "level.tscn")).To(ContainSubstring("<<<<<<<"))

		notGodot := m.callToolErr("nipa_godot_merge", map[string]any{"repo": dir, "path": "notes.txt"})
		Expect(notGodot).To(ContainSubstring("not a Godot text scene"))

		var out struct {
			Path      string `json:"path"`
			Clean     bool   `json:"clean"`
			Conflicts []any  `json:"conflicts"`
			Status    *struct {
				Staged    []string `json:"staged"`
				Conflicts []string `json:"conflicts"`
			} `json:"status"`
		}
		m.callToolOK("nipa_godot_merge", map[string]any{"repo": dir, "path": "level.tscn"}, &out)
		Expect(out.Clean).To(BeTrue())
		Expect(out.Conflicts).To(BeEmpty())
		Expect(out.Status).NotTo(BeNil())
		Expect(out.Status.Staged).To(ContainElement("level.tscn"))

		text := readText(dir, "level.tscn")
		Expect(text).NotTo(ContainSubstring("<<<<<<<"))
		Expect(text).To(ContainSubstring("position = Vector2(16, 0)"))
		Expect(text).To(ContainSubstring("position = Vector2(0, 32)"))
		Expect(text).To(ContainSubstring(`[node name="Coin"`))
		Expect(text).To(ContainSubstring(`[node name="Door"`))

		var conflicts struct {
			Files []struct {
				Path     string `json:"path"`
				Resolved bool   `json:"resolved"`
			} `json:"files"`
		}
		m.callToolOK("nipa_conflicts", map[string]any{"repo": dir}, &conflicts)
		Expect(conflicts.Files).To(HaveLen(1))
		Expect(conflicts.Files[0].Path).To(Equal("level.tscn"))
		Expect(conflicts.Files[0].Resolved).To(BeTrue())

		Expect(pushRepo(dir, "merge feature").ExitCode).To(Equal(0))
		var status statusJSON
		m.callToolOK("nipa_status", map[string]any{"repo": dir}, &status)
		Expect(status.Conflicts).To(BeEmpty())
		Expect(status.Staged).To(BeEmpty())
		check := cloneRepo(workspace(), url, "check")
		Expect(readText(check, "level.tscn")).To(Equal(text))
	})

	It("reports Godot property conflicts without writing the scene", func() {
		dir, _ := divergeGodotScene(moveNode(godotBaseScene, "A", "16, 0"), moveNode(godotBaseScene, "A", "0, 48"))

		m := startMCPWithArgs(dir, cli.tokenFile, "--allow", "merge")
		var merged struct {
			Conflicts []string `json:"conflicts"`
		}
		m.callToolOK("nipa_merge", map[string]any{"repo": dir, "source": "feature"}, &merged)
		Expect(merged.Conflicts).To(ConsistOf("level.tscn"))
		before := readText(dir, "level.tscn")

		var out struct {
			Clean     bool `json:"clean"`
			Conflicts []struct {
				Kind string `json:"kind"`
			} `json:"conflicts"`
			Status *json.RawMessage `json:"status"`
			Next   string           `json:"next"`
		}
		result := m.callToolOK("nipa_godot_merge", map[string]any{"repo": dir, "path": "level.tscn"}, &out)
		Expect(out.Clean).To(BeFalse())
		Expect(out.Conflicts).NotTo(BeEmpty(), string(result.StructuredContent))
		Expect(out.Status).To(BeNil())
		Expect(out.Next).To(ContainSubstring("nipa_resolve_conflict"))
		Expect(readText(dir, "level.tscn")).To(Equal(before))

		var status statusJSON
		runJSONInto(dir, &status, "status", "--json")
		Expect(status.Conflicts).To(ContainElement("level.tscn"))

		var aborted struct {
			Aborted bool `json:"aborted"`
		}
		m.callToolOK("nipa_merge", map[string]any{"repo": dir, "abort": true}, &aborted)
		Expect(aborted.Aborted).To(BeTrue())
		Expect(readText(dir, "level.tscn")).To(Equal(moveNode(godotBaseScene, "A", "0, 48")))
	})

	It("locks, queries and unlocks branch paths over MCP", func() {
		project := newProject("mcp-locks")
		seedRepo(orgSlug, project, map[string][]byte{"art/hero.png": testPNG(2)}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))

		m := startMCPWithArgs(dir, cli.tokenFile, "--allow", "lock")
		var lock struct {
			Path   string `json:"path"`
			Scope  string `json:"scope"`
			Branch string `json:"branch"`
		}
		m.callToolOK("nipa_lock", map[string]any{"repo": dir, "path": "art"}, &lock)
		Expect(lock.Path).To(Equal("art"))
		Expect(lock.Scope).To(Equal("branch"))
		Expect(lock.Branch).To(Equal("feature"))

		var global struct {
			Scope string `json:"scope"`
		}
		m.callToolOK("nipa_lock", map[string]any{"repo": dir, "path": "docs/readme.md", "branch": "main"}, &global)
		Expect(global.Scope).NotTo(Equal("branch"))

		var onFeature mcpLockQueryJSON
		m.callToolOK("nipa_lock_query", map[string]any{"repo": dir, "path": "art/hero.png"}, &onFeature)
		Expect(onFeature.Branch).To(Equal("feature"))
		Expect(onFeature.Scope).To(Equal("branch"))
		Expect(onFeature.Locked).To(BeTrue())
		Expect(onFeature.Locks).To(HaveLen(1))
		Expect(onFeature.Locks[0].Path).To(Equal("art"))
		Expect(onFeature.OtherScopes).To(BeEmpty())

		var onMain mcpLockQueryJSON
		m.callToolOK("nipa_lock_query", map[string]any{"repo": dir, "path": "art/hero.png", "branch": "main"}, &onMain)
		Expect(onMain.Scope).To(Equal("mainline"))
		Expect(onMain.Locked).To(BeFalse())
		Expect(onMain.OtherScopes).To(HaveLen(1))
		Expect(onMain.OtherScopes[0].Branch).To(Equal("feature"))

		var mainlineDoc mcpLockQueryJSON
		m.callToolOK("nipa_lock_query", map[string]any{"repo": dir, "path": "docs/readme.md", "branch": "main"}, &mainlineDoc)
		Expect(mainlineDoc.Locked).To(BeTrue())

		var unlocked struct {
			Path string `json:"path"`
		}
		m.callToolOK("nipa_unlock", map[string]any{"repo": dir, "path": "art"}, &unlocked)
		Expect(unlocked.Path).To(Equal("art"))

		var after mcpLockQueryJSON
		m.callToolOK("nipa_lock_query", map[string]any{"repo": dir, "path": "art/hero.png"}, &after)
		Expect(after.Locked).To(BeFalse())
		Expect(after.Locks).To(BeEmpty())

		missing := m.callToolErr("nipa_lock_query", map[string]any{"repo": dir, "path": " "})
		Expect(missing).To(ContainSubstring("a path is required"))

		var locks locksJSON
		runJSONInto(dir, &locks, "lock", "list", "--json")
		Expect(locks.Locks).To(HaveLen(1))
		Expect(locks.Locks[0].Path).To(Equal("docs/readme.md"))
	})
})
