package suite_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const comfyWorkflow = `{"3": {"class_type": "KSampler", "inputs": {"seed": 1234, "positive": ["6", 0]}}, "4": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "sdxl.safetensors"}}, "6": {"class_type": "CLIPTextEncode", "inputs": {"text": "a knight"}}}`

func comfyPNG(side int) []byte {
	GinkgoHelper()
	base := testPNG(side)
	data := append([]byte("prompt\x00"), comfyWorkflow...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("tEXt"), data...)))
	out := append([]byte(nil), base[:33]...)
	out = append(out, chunk...)
	return append(out, base[33:]...)
}

type provenanceReportJSON struct {
	Since string `json:"since"`
	Files []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
		Record *struct {
			Tool         string `json:"tool"`
			Model        string `json:"model"`
			Seed         uint64 `json:"seed"`
			PromptSHA256 string `json:"prompt_sha256"`
			Source       string `json:"source"`
		} `json:"record"`
	} `json:"files"`
}

type provenanceShowJSON struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Record *struct {
		ContentHash  string  `json:"content_hash"`
		Tool         string  `json:"tool"`
		Model        string  `json:"model"`
		PromptSHA256 string  `json:"prompt_sha256"`
		Seed         *uint64 `json:"seed"`
		License      string  `json:"license"`
		HumanEdited  bool    `json:"human_edited"`
		Source       string  `json:"source"`
		C2PAVerified *bool   `json:"c2pa_verified"`
		RecordedAt   string  `json:"recorded_at"`
		Note         string  `json:"note"`
	} `json:"record"`
}

func provenanceShow(dir, path string) provenanceShowJSON {
	GinkgoHelper()
	var out provenanceShowJSON
	runJSONInto(dir, &out, "provenance", "show", path, "--json")
	return out
}

var _ = Describe("nipa provenance", func() {
	It("records generated images on add, follows renames and reports since a tag", func() {
		project := newProject("provenance")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaassets.yml": []byte("rules:\n  - name: generated\n    paths: [\"art/**\"]\n    require_provenance: true\n"),
			"a.txt":           []byte("a\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		initRes := runNipa(dir, "provenance", "init")
		Expect(initRes.ExitCode).To(Equal(0), initRes.Output())

		writeBytes(dir, "art/hero.png", comfyPNG(4))
		add := runNipa(dir, "add", "art/hero.png")
		Expect(add.ExitCode).To(Equal(0), add.Output())
		Expect(add.Stdout).To(ContainSubstring("provenance: recorded 1, moved 0"))
		Expect(readText(dir, ".nipaprovenance.yml")).To(ContainSubstring("tool: ComfyUI"))
		Expect(readText(dir, ".nipaprovenance.yml")).NotTo(ContainSubstring("a knight"))
		status := runNipa(dir, "status")
		Expect(status.Output()).To(ContainSubstring("A  .nipaprovenance.yml"))

		show := runNipa(dir, "provenance", "show", "art/hero.png", "--json")
		Expect(show.ExitCode).To(Equal(0), show.Output())
		Expect(show.Stdout).To(MatchRegexp(`"status":\s*"recorded"`))

		push := runNipa(dir, "push", "-m", uniqueMessage("generated hero"))
		Expect(push.ExitCode).To(Equal(0), push.Output())
		Expect(runNipa(dir, "tag", "-c", "v1").ExitCode).To(Equal(0))

		writeBytes(dir, "art/other.png", testPNG(4))
		Expect(runNipa(dir, "add", "art/other.png").ExitCode).To(Equal(0))
		dry := runNipa(dir, "push", "--dry-run", "--json")
		Expect(dry.ExitCode).To(Equal(0), dry.Output())
		var plan struct {
			AssetViolations []struct {
				Path    string `json:"path"`
				Limit   string `json:"limit"`
				Message string `json:"message"`
			} `json:"asset_violations"`
		}
		Expect(json.Unmarshal([]byte(dry.Stdout), &plan)).To(Succeed())
		Expect(plan.AssetViolations).To(HaveLen(1))
		Expect(plan.AssetViolations[0].Path).To(Equal("art/other.png"))
		Expect(plan.AssetViolations[0].Limit).To(Equal("require_provenance"))

		Expect(os.Rename(filepath.Join(dir, "art", "hero.png"), filepath.Join(dir, "art", "knight.png"))).To(Succeed())
		moved := runNipa(dir, "add", "art")
		Expect(moved.ExitCode).To(Equal(0), moved.Output())
		Expect(moved.Stdout).To(ContainSubstring("moved 1"))
		manifest := readText(dir, ".nipaprovenance.yml")
		Expect(manifest).To(ContainSubstring("art/knight.png:"))
		Expect(manifest).NotTo(ContainSubstring("art/hero.png:"))

		report := runNipa(dir, "provenance", "report", "--since", "v1", "--json")
		Expect(report.ExitCode).To(Equal(0), report.Output())
		var out provenanceReportJSON
		Expect(json.Unmarshal([]byte(report.Stdout), &out)).To(Succeed())
		Expect(out.Since).To(Equal("v1"))
		byPath := map[string]string{}
		for _, f := range out.Files {
			byPath[f.Path] = f.Status
			if f.Path == "art/knight.png" {
				Expect(f.Record).NotTo(BeNil())
				Expect(f.Record.Tool).To(Equal("ComfyUI"))
				Expect(f.Record.Seed).To(Equal(uint64(1234)))
			}
		}
		Expect(byPath).To(Equal(map[string]string{"art/knight.png": "recorded", "art/other.png": "missing"}))

		m := startMCP(dir, false)
		res := m.callTool("nipa_provenance", map[string]any{"repo": dir, "since": "v1"})
		Expect(res.IsError).To(BeFalse(), res.text())
		var tool struct {
			Records []struct {
				Path string `json:"path"`
			} `json:"records"`
			Missing []string `json:"missing"`
		}
		res.decode(&tool)
		Expect(tool.Records).To(HaveLen(1))
		Expect(tool.Missing).To(Equal([]string{"art/other.png"}))
	})

	It("keeps the provenance file in sparse clones", func() {
		project := newProject("provenance-sparse")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaprovenance.yml": []byte("version: 1\nfiles: {}\n"),
			"src/a.txt":           []byte("a\n"),
			"docs/b.txt":          []byte("b\n"),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "sparse", "--sparse", "src")
		_, err := os.Stat(filepath.Join(dir, ".nipaprovenance.yml"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("records a manual record with every set flag and round-trips it through show and a clone", func() {
		project := newProject("provenance-set")
		seedRepo(orgSlug, project, map[string][]byte{
			"art/hero.png": testPNG(4),
			"a.txt":        []byte("a\n"),
		}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		Expect(fileExists(dir, ".nipaprovenance.yml")).To(BeFalse())

		promptFile := filepath.Join(workspace(), "prompt.txt")
		Expect(os.WriteFile(promptFile, []byte("  a red knight on a hill \n"), 0o644)).To(Succeed())
		sum := sha256.Sum256([]byte("a red knight on a hill"))
		promptHash := hex.EncodeToString(sum[:])

		res := runNipa(dir, "provenance", "set", "./art/hero.png",
			"--tool", " ComfyUI ", "--model", "sdxl", "--seed", "42", "--prompt-file", promptFile,
			"--license", "CC-BY-4.0", "--note", "first pass")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Stdout).To(ContainSubstring("recorded provenance for art/hero.png"))

		manifest := readText(dir, ".nipaprovenance.yml")
		Expect(manifest).To(ContainSubstring("art/hero.png:"))
		Expect(manifest).To(ContainSubstring("source: manual"))
		Expect(manifest).To(ContainSubstring(promptHash))
		Expect(manifest).NotTo(ContainSubstring("red knight"), "only the prompt hash is stored")
		status := runNipa(dir, "status")
		Expect(status.Output()).To(ContainSubstring("A  .nipaprovenance.yml"), "set creates and stages the file")

		show := provenanceShow(dir, "art/hero.png")
		Expect(show.Path).To(Equal("art/hero.png"))
		Expect(show.Status).To(Equal("recorded"))
		Expect(show.Record).NotTo(BeNil())
		Expect(show.Record.Tool).To(Equal("ComfyUI"), "values are trimmed")
		Expect(show.Record.Model).To(Equal("sdxl"))
		Expect(show.Record.Seed).NotTo(BeNil())
		Expect(*show.Record.Seed).To(Equal(uint64(42)))
		Expect(show.Record.PromptSHA256).To(Equal(promptHash))
		Expect(show.Record.License).To(Equal("CC-BY-4.0"))
		Expect(show.Record.Note).To(Equal("first pass"))
		Expect(show.Record.HumanEdited).To(BeFalse())
		Expect(show.Record.Source).To(Equal("manual"))
		Expect(show.Record.C2PAVerified).To(BeNil())
		Expect(show.Record.ContentHash).To(MatchRegexp(`^blake3:[0-9a-f]{64}$`))
		Expect(show.Record.RecordedAt).NotTo(BeEmpty())

		text := runNipa(dir, "provenance", "show", "art/hero.png")
		Expect(text.ExitCode).To(Equal(0), text.Output())
		Expect(text.Stdout).To(ContainSubstring("art/hero.png: recorded"))
		Expect(text.Stdout).To(MatchRegexp(`(?m)^  tool:\s+ComfyUI$`))
		Expect(text.Stdout).To(MatchRegexp(`(?m)^  seed:\s+42$`))
		Expect(text.Stdout).To(MatchRegexp(`(?m)^  source:\s+manual$`))
		Expect(text.Stdout).To(MatchRegexp(`(?m)^  note:\s+first pass$`))

		// Only the given flags change; the others keep their values.
		res = runNipa(dir, "provenance", "set", "art/hero.png", "--model", "sdxl-turbo", "--human-edited")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		show = provenanceShow(dir, "art/hero.png")
		Expect(show.Record.Model).To(Equal("sdxl-turbo"))
		Expect(show.Record.Tool).To(Equal("ComfyUI"))
		Expect(*show.Record.Seed).To(Equal(uint64(42)))
		Expect(show.Record.PromptSHA256).To(Equal(promptHash))
		Expect(show.Record.Note).To(Equal("first pass"))
		Expect(show.Record.HumanEdited).To(BeTrue())
		Expect(show.Status).To(Equal("recorded"))

		// A changed file marked human_edited reads as edited; set refreshes
		// the content hash, and without the mark a change reads as modified.
		writeBytes(dir, "art/hero.png", testPNG(6))
		Expect(provenanceShow(dir, "art/hero.png").Status).To(Equal("edited"))
		res = runNipa(dir, "provenance", "set", "art/hero.png", "--human-edited=false", "--note", "")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		show = provenanceShow(dir, "art/hero.png")
		Expect(show.Status).To(Equal("recorded"))
		Expect(show.Record.HumanEdited).To(BeFalse())
		Expect(show.Record.Note).To(BeEmpty())
		writeBytes(dir, "art/hero.png", testPNG(8))
		Expect(provenanceShow(dir, "art/hero.png").Status).To(Equal("modified"))
		res = runNipa(dir, "provenance", "set", "art/hero.png")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		final := provenanceShow(dir, "art/hero.png")
		Expect(final.Status).To(Equal("recorded"))

		missing := provenanceShow(dir, "a.txt")
		Expect(missing.Status).To(Equal("missing"))
		Expect(missing.Record).To(BeNil())

		Expect(runNipa(dir, "add", "art/hero.png").ExitCode).To(Equal(0))
		lock := runNipa(dir, "lock", "art/hero.png")
		Expect(lock.ExitCode).To(Equal(0), lock.Output())
		push := pushRepo(dir, "record hero provenance")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		other := cloneRepo(parent, url, "other")
		cloned := provenanceShow(other, "art/hero.png")
		Expect(cloned.Status).To(Equal("recorded"), "the record travels with the project")
		Expect(cloned.Record.ContentHash).To(Equal(final.Record.ContentHash))
		Expect(cloned.Record.Model).To(Equal("sdxl-turbo"))
		Expect(cloned.Record.PromptSHA256).To(Equal(promptHash))

		report := runNipa(other, "provenance", "report", "--json")
		Expect(report.ExitCode).To(Equal(0), report.Output())
		var out provenanceReportJSON
		Expect(json.Unmarshal([]byte(report.Stdout), &out)).To(Succeed())
		Expect(out.Files).To(HaveLen(1))
		Expect(out.Files[0].Path).To(Equal("art/hero.png"))
		Expect(out.Files[0].Status).To(Equal("recorded"))
		Expect(out.Files[0].Record.Source).To(Equal("manual"))
		Expect(out.Files[0].Record.Seed).To(Equal(uint64(42)))
	})

	It("adds a record to an existing provenance file without touching the others", func() {
		project := newProject("provenance-set-existing")
		seedRepo(orgSlug, project, map[string][]byte{
			".nipaprovenance.yml": []byte("version: 1\nfiles:\n  art/old.png:\n    tool: Firefly\n    human_edited: false\n"),
			"art/old.png":         testPNG(4),
			"art/new.png":         testPNG(5),
		}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		res := runNipa(dir, "provenance", "set", "art/new.png", "--tool", "ElevenLabs")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		old := provenanceShow(dir, "art/old.png")
		Expect(old.Record).NotTo(BeNil())
		Expect(old.Record.Tool).To(Equal("Firefly"))
		Expect(old.Record.Source).To(BeEmpty(), "other records are left as they were")
		Expect(provenanceShow(dir, "art/new.png").Record.Tool).To(Equal("ElevenLabs"))
		Expect(runNipa(dir, "status").Output()).To(ContainSubstring("A  .nipaprovenance.yml"), "set stages the updated file")
	})

	It("rejects invalid paths, missing inputs and broken provenance files", func() {
		project := newProject("provenance-set-errors")
		seedRepo(orgSlug, project, map[string][]byte{"art/hero.png": testPNG(4)}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		expectFailure := func(substr string, args ...string) {
			GinkgoHelper()
			res := runNipa(dir, append([]string{"provenance", "set"}, args...)...)
			Expect(res.ExitCode).To(Equal(1), res.Output())
			Expect(res.Output()).To(ContainSubstring(substr))
		}
		expectFailure("accepts 1 arg(s)")
		expectFailure("accepts 1 arg(s)", "a.png", "b.png")
		expectFailure("cannot read art/missing.png", "art/missing.png")
		expectFailure("cannot record the provenance of .nipaprovenance.yml", ".nipaprovenance.yml")
		expectFailure("must not contain a .. segment", "../outside.png")
		expectFailure("invalid path", ".")
		expectFailure("read prompt file", "art/hero.png", "--prompt-file", filepath.Join(workspace(), "absent.txt"))
		expectFailure("invalid argument", "art/hero.png", "--seed", "-1")
		Expect(fileExists(dir, ".nipaprovenance.yml")).To(BeFalse(), "failed sets create no provenance file")

		show := runNipa(dir, "provenance", "show", "art/hero.png")
		Expect(show.ExitCode).To(Equal(1), show.Output())
		Expect(show.Output()).To(ContainSubstring("no .nipaprovenance.yml; run nipa provenance init"))

		writeText(dir, ".nipaprovenance.yml", "version: 2\nfiles: {}\n")
		res := runNipa(dir, "provenance", "set", "art/hero.png", "--tool", "x")
		Expect(res.ExitCode).NotTo(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("unsupported version 2"))
		Expect(readText(dir, ".nipaprovenance.yml")).To(Equal("version: 2\nfiles: {}\n"), "a broken file is not overwritten")

		outside := runNipa(workspace(), "provenance", "set", "art/hero.png")
		Expect(outside.ExitCode).To(Equal(1), outside.Output())
		Expect(outside.Output()).To(ContainSubstring("not a nipa repository"))
	})
})

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
