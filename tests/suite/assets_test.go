package suite_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const assetBudget = `rules:
  - name: textures
    paths: ["*.png"]
    power_of_two: true
    max_texture_size: 16
`

var _ = Describe("nipa assets check", func() {
	It("reports budget violations, in dry runs and through MCP", func() {
		project := newProject("assets-check")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaassets.yml": []byte(assetBudget),
			"tex.png":         testPNG(4),
			"src/a.txt":       []byte("a\n"),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		clean := runNipa(dir, "assets", "check")
		Expect(clean.ExitCode).To(Equal(0), clean.Output())
		Expect(clean.Output()).To(ContainSubstring("0 violation(s)"))

		writeBytes(dir, "tex.png", testPNG(6))
		writeBytes(dir, "big.png", testPNG(32))
		Expect(runNipa(dir, "add", "big.png").ExitCode).To(Equal(0))

		res := runNipa(dir, "assets", "check")
		Expect(res.ExitCode).To(Equal(2), res.Output())
		Expect(res.Stdout).To(ContainSubstring("tex.png: power_of_two: 6x6 is not a power of two (rule textures)"))
		Expect(res.Stdout).To(ContainSubstring("big.png: max_texture_size: 32x32 exceeds 16px (rule textures)"))
		Expect(res.Stderr).To(BeEmpty())

		jsonRes := runNipa(dir, "assets", "check", "--staged", "--json")
		Expect(jsonRes.ExitCode).To(Equal(2), jsonRes.Output())
		var report struct {
			Config     bool `json:"config"`
			Checked    int  `json:"checked"`
			Violations []struct {
				Path  string `json:"path"`
				Limit string `json:"limit"`
				Rule  string `json:"rule"`
			} `json:"violations"`
		}
		Expect(json.Unmarshal([]byte(jsonRes.Stdout), &report)).To(Succeed())
		Expect(report.Config).To(BeTrue())
		Expect(report.Violations).To(HaveLen(1))
		Expect(report.Violations[0].Path).To(Equal("big.png"))
		Expect(report.Violations[0].Limit).To(Equal("max_texture_size"))

		dry := runNipa(dir, "push", "--dry-run", "--json")
		Expect(dry.ExitCode).To(Equal(0), dry.Output())
		var plan struct {
			AssetViolations []struct {
				Path string `json:"path"`
			} `json:"asset_violations"`
		}
		Expect(json.Unmarshal([]byte(dry.Stdout), &plan)).To(Succeed())
		Expect(plan.AssetViolations).To(HaveLen(1))
		Expect(plan.AssetViolations[0].Path).To(Equal("big.png"))

		m := startMCP(dir, false)
		check := m.callTool("nipa_asset_check", map[string]any{"repo": dir})
		Expect(check.IsError).To(BeFalse(), check.text())
		var out struct {
			Violations []struct {
				Path string `json:"path"`
			} `json:"violations"`
		}
		check.decode(&out)
		Expect(out.Violations).To(HaveLen(2))
	})

	It("keeps the budget file in sparse clones", func() {
		project := newProject("assets-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaassets.yml": []byte(assetBudget),
			"src/a.txt":       []byte("a\n"),
			"docs/b.txt":      []byte("b\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "sparse", "--sparse", "src")
		_, err := os.Stat(filepath.Join(dir, ".nipaassets.yml"))
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(filepath.Join(dir, "docs", "b.txt"))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("warns about broken asset references in dry runs and through MCP", func() {
		project := newProject("assets-refs")
		tex := strings.Repeat("a", 32)
		seedRepo(orgSlug, project, map[string][]byte{
			"Assets/hero.png":      testPNG(4),
			"Assets/hero.png.meta": []byte("fileFormatVersion: 2\nguid: " + tex + "\n"),
			"Assets/hero.mat":      []byte("%YAML 1.1\n--- !u!21 &2100000\nMaterial:\n  m_Tex: {fileID: 2800000, guid: " + tex + ", type: 3}\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		m := startMCP(dir, false)
		refs := m.callTool("nipa_asset_refs", map[string]any{"repo": dir, "path": "Assets/hero.png"})
		Expect(refs.IsError).To(BeFalse(), refs.text())
		var used struct {
			UsedBy []string `json:"used_by"`
		}
		refs.decode(&used)
		Expect(used.UsedBy).To(Equal([]string{"Assets/hero.mat"}))

		Expect(runNipa(dir, "lock", "Assets/hero.png").ExitCode).To(Equal(0))
		Expect(os.Remove(filepath.Join(dir, "Assets", "hero.png"))).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "Assets", "hero.png.meta"))).To(Succeed())
		Expect(runNipa(dir, "add", "Assets/hero.png", "Assets/hero.png.meta").ExitCode).To(Equal(0))
		type broken struct {
			From   string `json:"from"`
			Target string `json:"target"`
			Reason string `json:"reason"`
		}
		want := broken{From: "Assets/hero.mat", Target: "Assets/hero.png", Reason: "removed"}

		dry := runNipa(dir, "push", "--dry-run", "--json")
		Expect(dry.ExitCode).To(Equal(0), dry.Output())
		var plan struct {
			AssetImpact struct {
				Broken []broken `json:"broken"`
			} `json:"asset_impact"`
		}
		Expect(json.Unmarshal([]byte(dry.Stdout), &plan)).To(Succeed())
		Expect(plan.AssetImpact.Broken).To(Equal([]broken{want}))

		impact := m.callTool("nipa_asset_impact", map[string]any{"repo": dir, "staged": true})
		Expect(impact.IsError).To(BeFalse(), impact.text())
		var out struct {
			Broken []broken `json:"broken"`
		}
		impact.decode(&out)
		Expect(out.Broken).To(Equal([]broken{want}))

		text := runNipa(dir, "push", "--dry-run")
		Expect(text.Stdout).To(ContainSubstring("warning: 1 broken reference(s)"))
		pushed := runNipa(dir, "push", "-m", "remove hero")
		Expect(pushed.ExitCode).To(Equal(0), pushed.Output())
	})

	It("exits 0 without a budget file", func() {
		project := newProject("assets-none")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		res := runNipa(dir, "assets", "check", "--json")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(MatchJSON(`{"config":false,"violations":[],"checked":0}`))
	})
})
