package cli

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/provenance"
)

const testComfyPrompt = `{"3": {"class_type": "KSampler", "inputs": {"seed": 7, "positive": ["6", 0]}}, "4": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "sdxl.safetensors"}}, "6": {"class_type": "CLIPTextEncode", "inputs": {"text": "castle"}}}`

func comfyPNG(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4))))
	base := buf.Bytes()
	data := append([]byte("prompt\x00"), testComfyPrompt...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("tEXt"), data...)))
	out := append([]byte(nil), base[:33]...)
	out = append(out, chunk...)
	return string(append(out, base[33:]...))
}

func newProvenanceCmd() *cobra.Command {
	return NewCli(&fakeUsecaseContainer{diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())}, &fakeConnector{}).setupProvenanceCmd()
}

func stagedPaths(t *testing.T, root string) []string {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer func() { _ = lr.Close() }()
	staged, err := lr.ListStaged()
	require.NoError(t, err)
	return staged
}

func TestProvenanceInitAndAdd(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	out, err := runCmdInDir(t, root, newProvenanceCmd(), "init")
	require.NoError(t, err)
	require.Equal(t, "created .nipaprovenance.yml\n", out)
	require.Contains(t, stagedPaths(t, root), provenance.FileName)
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "init")
	require.ErrorContains(t, err, "already exists")

	writeFile(t, root, "art/hero.png", comfyPNG(t))
	writeFile(t, root, "art/skip.png", comfyPNG(t)+"x")
	cli := NewCli(&fakeUsecaseContainer{}, &fakeConnector{})
	out, err = runCmdInDir(t, root, cli.setupAddCmd(), "art/hero.png")
	require.NoError(t, err)
	require.Equal(t, "provenance: recorded 1, moved 0, deleted 0\n", out)
	out, err = runCmdInDir(t, root, cli.setupAddCmd(), "--no-provenance", "art/skip.png")
	require.NoError(t, err)
	require.Empty(t, out)

	out, err = runCmdInDir(t, root, newProvenanceCmd(), "show", "art/hero.png")
	require.NoError(t, err)
	require.Contains(t, out, "art/hero.png: recorded\n")
	require.Contains(t, out, "tool:           ComfyUI\n")
	require.Contains(t, out, "seed:           7\n")

	out, err = runCmdInDir(t, root, newProvenanceCmd(), "show", "art/skip.png", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"path":"art/skip.png","record":null,"status":"missing"}`, out)

	writeFile(t, root, "art/hero.png", comfyPNG(t)+"edit")
	out, err = runCmdInDir(t, root, newProvenanceCmd(), "show", "art/hero.png", "--json")
	require.NoError(t, err)
	var show provenanceShow
	require.NoError(t, json.Unmarshal([]byte(out), &show))
	require.Equal(t, provenance.StatusModified, show.Status)
	require.Equal(t, "sdxl.safetensors", show.Record.Model)
}

func TestProvenanceShowErrors(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	_, err := runCmdInDir(t, root, newProvenanceCmd(), "show", "a.txt")
	require.ErrorContains(t, err, "run nipa provenance init")
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "show", "..")
	require.Error(t, err)
	writeFile(t, root, provenance.FileName, "version: 4\n")
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "show", "a.txt")
	require.ErrorContains(t, err, "unsupported version 4")
	for _, sub := range []string{"show", "set", "init", "report"} {
		args := []string{sub}
		if sub == "show" || sub == "set" {
			args = append(args, "a.txt")
		}
		_, err = runCmdInDir(t, t.TempDir(), newProvenanceCmd(), args...)
		require.Error(t, err, sub)
	}
}

func TestProvenanceSet(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "voice.wav", "audio")
	prompt := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(prompt, []byte("  say hello\n"), 0o644))

	out, err := runCmdInDir(t, root, newProvenanceCmd(), "set", "voice.wav", "--tool", "ElevenLabs", "--model", "v2", "--seed", "9",
		"--prompt-file", prompt, "--license", "commercial", "--human-edited", "--note", "trimmed")
	require.NoError(t, err)
	require.Equal(t, "recorded provenance for voice.wav\n", out)
	require.Contains(t, stagedPaths(t, root), provenance.FileName)
	data, err := os.ReadFile(filepath.Join(root, provenance.FileName))
	require.NoError(t, err)
	require.NotContains(t, string(data), "say hello")

	f, err := usecase.LoadProvenance(root)
	require.NoError(t, err)
	rec := f.Get("voice.wav")
	require.Equal(t, "ElevenLabs", rec.Tool)
	require.Equal(t, uint64(9), *rec.Seed)
	require.Equal(t, provenance.PromptHash("say hello"), rec.PromptSHA256)
	require.True(t, rec.HumanEdited)
	require.Equal(t, provenance.SourceManual, rec.Source)
	require.Equal(t, provenance.HashBytes([]byte("audio")), rec.ContentHash)

	writeFile(t, root, "voice.wav", "audio v2")
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "set", "voice.wav", "--human-edited=false")
	require.NoError(t, err)
	f, err = usecase.LoadProvenance(root)
	require.NoError(t, err)
	rec = f.Get("voice.wav")
	require.Equal(t, "ElevenLabs", rec.Tool, "unset flags keep their value")
	require.False(t, rec.HumanEdited)
	require.Equal(t, provenance.HashBytes([]byte("audio v2")), rec.ContentHash)

	_, err = runCmdInDir(t, root, newProvenanceCmd(), "set", "missing.wav")
	require.ErrorContains(t, err, "cannot read missing.wav")
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "set", "voice.wav", "--prompt-file", filepath.Join(root, "nope"))
	require.ErrorContains(t, err, "read prompt file")
	writeFile(t, root, provenance.FileName, "version: 4\n")
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "set", "voice.wav")
	require.ErrorContains(t, err, "unsupported version 4")
}

func TestProvenanceReport(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	out, err := runCmdInDir(t, root, newProvenanceCmd(), "report")
	require.NoError(t, err)
	require.Equal(t, "no .nipaprovenance.yml; every file is missing a record\n0 file(s): 0 recorded, 0 modified, 0 edited, 0 missing\n", out)

	writeFile(t, root, ".nipaassets.yml", "rules:\n  - name: art\n    paths: [\"art/**\"]\n    require_provenance: true\n")
	writeFile(t, root, "art/hero.png", comfyPNG(t))
	writeFile(t, root, "art/other.png", testPNG(t, 2, 2))
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "init")
	require.NoError(t, err)
	_, err = runCmdInDir(t, root, NewCli(&fakeUsecaseContainer{}, &fakeConnector{}).setupAddCmd(), "art")
	require.NoError(t, err)
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "set", "a.txt", "--tool", "Writer", "--license", "CC0")
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(root, "a.txt")))

	out, err = runCmdInDir(t, root, newProvenanceCmd(), "report")
	require.NoError(t, err)
	require.Contains(t, out, "recorded  art/hero.png  (ComfyUI, sdxl.safetensors)\n")
	require.Contains(t, out, "missing   art/other.png\n")
	require.Contains(t, out, "orphaned  a.txt\n")
	require.Contains(t, out, "2 file(s): 1 recorded, 0 modified, 0 edited, 1 missing\n")
	require.Contains(t, out, "tools:\n  ComfyUI: 1\nmodels:\n  sdxl.safetensors: 1\n")

	out, err = runCmdInDir(t, root, newProvenanceCmd(), "report", "--json", "art/hero.png")
	require.NoError(t, err)
	var report assetscan.ProvenanceReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Len(t, report.Files, 1)
	require.Equal(t, "ComfyUI", report.Files[0].Record.Tool)

	noDiff := NewCli(&fakeUsecaseContainer{}, &fakeConnector{}).setupProvenanceCmd()
	_, err = runCmdInDir(t, root, noDiff, "report", "--since", "main")
	require.ErrorContains(t, err, "diff is not available")
	_, err = runCmdInDir(t, root, newProvenanceCmd(), "report", "..")
	require.Error(t, err)
}

func TestProvenanceReport_Since(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "hero.png", comfyPNG(t))
	_, err := runCmdInDir(t, root, newProvenanceCmd(), "init")
	require.NoError(t, err)
	_, err = runCmdInDir(t, root, NewCli(&fakeUsecaseContainer{}, &fakeConnector{}).setupAddCmd(), "hero.png")
	require.NoError(t, err)

	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	diffClient := &fakeRevDiffClient{branches: map[string]string{"main": "a"}, commits: map[string]map[string]string{"a": {"a.txt": "one\n"}}}
	cmd := NewCli(&fakeUsecaseContainer{diff: usecase.NewDiff(auth, diffClient, localrepo.NewLocalRepo())}, &fakeConnector{}).setupProvenanceCmd()
	out, err := runCmdInDir(t, root, cmd, "report", "--since", "main", "--json")
	require.NoError(t, err)
	var report assetscan.ProvenanceReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Equal(t, "main", report.Since)
	require.Len(t, report.Files, 1)
	require.Equal(t, "hero.png", report.Files[0].Path)
	require.Equal(t, provenance.StatusRecorded, report.Files[0].Status)
}

func TestSetupPushCmd_DryRunProvenanceViolation(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, ".nipaassets.yml", "rules:\n  - name: art\n    paths: [\"*.png\"]\n    require_provenance: true\n")
	writeFile(t, root, "tex.png", testPNG(t, 4, 4))
	stagePath(t, root, "tex.png")

	out, err := runCmdInDir(t, root, newPushDiffCli(t).setupPushCmd(), "--dry-run", "--json")
	require.NoError(t, err)
	var plan output.Plan
	require.NoError(t, json.Unmarshal([]byte(out), &plan))
	require.Len(t, plan.AssetViolations, 1)
	require.Equal(t, "require_provenance", plan.AssetViolations[0].Limit)
	require.Equal(t, "no provenance record", plan.AssetViolations[0].Message)

	_, err = runCmdInDir(t, root, newProvenanceCmd(), "set", "tex.png", "--tool", "Painter")
	require.NoError(t, err)
	out, err = runCmdInDir(t, root, newPushDiffCli(t).setupPushCmd(), "--dry-run", "--json")
	require.NoError(t, err)
	require.NotContains(t, out, "asset_violations")
}
