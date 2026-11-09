package usecase

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/provenance"
)

const comfyText = `{"3": {"class_type": "KSampler", "inputs": {"seed": 42, "positive": ["6", 0]}}, "4": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "sdxl.safetensors"}}, "6": {"class_type": "CLIPTextEncode", "inputs": {"text": "a dragon"}}}`

func generatedPNG(t *testing.T, key, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))))
	base := buf.Bytes()
	data := append(append([]byte(key), 0), text...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("tEXt"), data...)))
	ihdrEnd := 8 + 8 + 13 + 4
	out := append([]byte(nil), base[:ihdrEnd]...)
	out = append(out, chunk...)
	return append(out, base[ihdrEnd:]...)
}

func readProvenance(t *testing.T, root string) *provenance.File {
	t.Helper()
	f, err := LoadProvenance(root)
	require.NoError(t, err)
	require.NotNil(t, f)
	return f
}

func optIn(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, SaveProvenance(root, provenance.New()))
}

func TestAddProvenance_OptInOff(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "hero.png", string(generatedPNG(t, "prompt", comfyText)))
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	rep, err := newWorkingCopy(t, local, root).AddWithReport(context.Background(), []string{"hero.png"})
	require.NoError(t, err)
	require.False(t, rep.Changed())
	require.Equal(t, []string{"hero.png"}, local.stageAdd)
	_, err = os.Stat(filepath.Join(root, provenance.FileName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAddProvenance_RecordsAndStages(t *testing.T) {
	root := t.TempDir()
	optIn(t, root)
	content := generatedPNG(t, "prompt", comfyText)
	writeRepoFile(t, root, "art/hero.png", string(content))
	writeRepoFile(t, root, "art/plain.png", string(generatedPNG(t, "Comment", "hi")))
	writeRepoFile(t, root, "notes.txt", "text")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	rep, err := newWorkingCopy(t, local, root).AddWithReport(context.Background(), []string{"art", "notes.txt"})
	require.NoError(t, err)
	require.Equal(t, ProvenanceReport{Recorded: 1}, rep)
	require.Equal(t, "provenance: recorded 1, moved 0, deleted 0", rep.String())
	require.Equal(t, []string{provenance.FileName, "art/hero.png", "art/plain.png", "notes.txt"}, local.stageAdd)

	rec := readProvenance(t, root).Get("art/hero.png")
	require.NotNil(t, rec)
	require.Equal(t, "ComfyUI", rec.Tool)
	require.Equal(t, "sdxl.safetensors", rec.Model)
	require.Equal(t, uint64(42), *rec.Seed)
	require.Equal(t, provenance.PromptHash("a dragon"), rec.PromptSHA256)
	require.Equal(t, provenance.HashBytes(content), rec.ContentHash)
	require.False(t, rec.RecordedAt.IsZero())
	data, err := os.ReadFile(filepath.Join(root, provenance.FileName))
	require.NoError(t, err)
	require.NotContains(t, string(data), "a dragon", "raw prompts are never stored")
}

func TestAddProvenance_KeepsExistingRecord(t *testing.T) {
	root := t.TempDir()
	f := provenance.New()
	require.NoError(t, f.Set("hero.png", &provenance.Record{Tool: "Manual", Source: provenance.SourceManual}))
	require.NoError(t, SaveProvenance(root, f))
	writeRepoFile(t, root, "hero.png", string(generatedPNG(t, "prompt", comfyText)))
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	rep, err := newWorkingCopy(t, local, root).AddWithReport(context.Background(), []string{"hero.png", provenance.FileName})
	require.NoError(t, err)
	require.False(t, rep.Changed())
	require.Equal(t, "Manual", readProvenance(t, root).Get("hero.png").Tool)
	require.Equal(t, []string{provenance.FileName, "hero.png"}, local.stageAdd)
}

func TestAddProvenance_RenameFollowAndDelete(t *testing.T) {
	root := t.TempDir()
	content := generatedPNG(t, "prompt", comfyText)
	f := provenance.New()
	require.NoError(t, f.Set("old/hero.png", &provenance.Record{Tool: "Manual", ContentHash: provenance.HashBytes(content), Source: provenance.SourceManual}))
	require.NoError(t, f.Set("gone.wav", &provenance.Record{Tool: "Voice", ContentHash: provenance.HashBytes([]byte("audio"))}))
	require.NoError(t, f.Set("kept.wav", &provenance.Record{Tool: "Voice", ContentHash: provenance.HashBytes([]byte("kept"))}))
	require.NoError(t, SaveProvenance(root, f))
	writeRepoFile(t, root, "new/hero.png", string(content))
	writeRepoFile(t, root, "voice.ogg", "different")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
		{Path: "old/hero.png"}, {Path: "gone.wav"}, {Path: "kept.wav"},
	}}}
	wc := newWorkingCopy(t, local, root)

	rep, err := wc.AddWithReport(context.Background(), []string{"new/hero.png", "old/hero.png", "gone.wav", "voice.ogg"})
	require.NoError(t, err)
	require.Equal(t, ProvenanceReport{Moved: 1, Deleted: 1}, rep)
	got := readProvenance(t, root)
	require.Nil(t, got.Get("old/hero.png"))
	require.Equal(t, "Manual", got.Get("new/hero.png").Tool, "moved records are not re-extracted")
	require.Nil(t, got.Get("gone.wav"))
	require.NotNil(t, got.Get("kept.wav"), "orphans outside the staged paths stay")
	require.Contains(t, local.stageAdd, provenance.FileName)
}

func TestAddProvenance_NoProvenance(t *testing.T) {
	root := t.TempDir()
	optIn(t, root)
	writeRepoFile(t, root, "hero.png", string(generatedPNG(t, "parameters", "p\nSteps: 1, Seed: 3")))
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	require.NoError(t, newWorkingCopy(t, local, root).Add(context.Background(), []string{"hero.png"}, AddOptions{NoProvenance: true}))
	require.Empty(t, readProvenance(t, root).Files)
	require.Equal(t, []string{"hero.png"}, local.stageAdd)
}

func TestAddProvenance_Errors(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, provenance.FileName, "version: 3\n")
	writeRepoFile(t, root, "hero.png", "x")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	err := newWorkingCopy(t, local, root).Add(context.Background(), []string{"hero.png"})
	require.ErrorContains(t, err, "unsupported version 3")
	require.Empty(t, local.stageAdd)

	optIn(t, root)
	writeRepoFile(t, root, "broken.png", "not a png")
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir.png"), 0o755))
	rep, err := newWorkingCopy(t, local, root).AddWithReport(context.Background(), []string{"broken.png"})
	require.NoError(t, err)
	require.False(t, rep.Changed())
}

func TestRemoveProvenance(t *testing.T) {
	root := t.TempDir()
	f := provenance.New()
	require.NoError(t, f.Set("new.png", &provenance.Record{Tool: "x"}))
	require.NoError(t, f.Set("tracked.png", &provenance.Record{Tool: "y"}))
	require.NoError(t, SaveProvenance(root, f))
	local := &stubLocalRepo{
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "tracked.png"}}},
		staged:   []string{"new.png", "tracked.png"},
	}
	wc := newWorkingCopy(t, local, root)
	require.NoError(t, wc.Remove(context.Background(), []string{"new.png", "tracked.png"}))
	got := readProvenance(t, root)
	require.Nil(t, got.Get("new.png"))
	require.NotNil(t, got.Get("tracked.png"))
	require.Equal(t, []string{provenance.FileName}, local.stageAdd)

	local.stageAdd = nil
	local.staged = []string{"tracked.png", provenance.FileName}
	require.NoError(t, wc.Remove(context.Background(), []string{""}))
	require.Empty(t, local.stageAdd, "unstaging the provenance file leaves it alone")

	local.staged = []string{"tracked.png"}
	require.NoError(t, wc.Remove(context.Background(), []string{"tracked.png"}))
	require.Empty(t, local.stageAdd)

	local.snapshotErr = errors.New("db gone")
	require.ErrorContains(t, wc.Remove(context.Background(), []string{"tracked.png"}), "db gone")
	local.snapshotErr = nil
	local.stageRemoveErr = errors.New("stage failed")
	require.NoError(t, SaveProvenance(root, f))
	local.staged = []string{"new.png"}
	require.ErrorContains(t, wc.Remove(context.Background(), []string{"new.png"}), "stage failed")
}

func TestRemoveProvenance_NotOptedIn(t *testing.T) {
	root := t.TempDir()
	local := &stubLocalRepo{staged: []string{"a.png"}}
	require.NoError(t, newWorkingCopy(t, local, root).Remove(context.Background(), []string{"a.png"}))
	require.Equal(t, []string{"a.png"}, local.stageRemove)
	require.Empty(t, local.stageAdd)
}

func TestProvenanceFileHelpers(t *testing.T) {
	root := t.TempDir()
	_, err := HashWorkingFile(root, "missing")
	require.Error(t, err)
	require.Error(t, SaveProvenance(filepath.Join(root, "missing"), provenance.New()))
	require.NoError(t, os.Mkdir(filepath.Join(root, provenance.FileName), 0o755))
	_, err = LoadProvenance(root)
	require.Error(t, err)
	require.Equal(t, []string{provenance.FileName, "a"}, withProvenanceFile([]string{"a"}))
	require.Equal(t, []string{provenance.FileName}, withProvenanceFile([]string{provenance.FileName}))
}

func TestWorkingCopy_TrackedPaths(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "b.png", "b")
	writeRepoFile(t, root, "a.png", "a")
	writeRepoFile(t, root, "untracked.png", "u")
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))
	local := &stubLocalRepo{
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "b.png"}, {Path: "gone.png"}, {Path: "dir"}}},
		staged:   []string{"a.png", "b.png"},
	}
	wc := newWorkingCopy(t, local, root)
	paths, err := wc.TrackedPaths()
	require.NoError(t, err)
	require.Equal(t, []string{"a.png", "b.png"}, paths)

	local.stagedErr = errors.New("staged failed")
	_, err = wc.TrackedPaths()
	require.ErrorContains(t, err, "staged failed")
	local.snapshotErr = errors.New("snapshot failed")
	_, err = wc.TrackedPaths()
	require.ErrorContains(t, err, "snapshot failed")
}

func TestSaveProvenance_KeepsFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	root := t.TempDir()
	target := filepath.Join(root, provenance.FileName)
	require.NoError(t, SaveProvenance(root, provenance.New()))
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, materializedMode(0), info.Mode().Perm())

	require.NoError(t, os.Chmod(target, 0o600))
	require.NoError(t, SaveProvenance(root, provenance.New()))
	info, err = os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAddProvenance_UntrackedMissingRecordIsNotAnOrphan(t *testing.T) {
	root := t.TempDir()
	content := generatedPNG(t, "Comment", "copy")
	f := provenance.New()
	require.NoError(t, f.Set("outside/hero.png", &provenance.Record{Tool: "Manual", ContentHash: provenance.HashBytes(content), Source: provenance.SourceManual}))
	require.NoError(t, SaveProvenance(root, f))
	writeRepoFile(t, root, "inside/copy.png", string(content))
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}

	rep, err := newWorkingCopy(t, local, root).AddWithReport(context.Background(), []string{"inside/copy.png"})
	require.NoError(t, err)
	require.False(t, rep.Changed())
	got := readProvenance(t, root)
	require.Equal(t, "Manual", got.Get("outside/hero.png").Tool, "a record outside the sparse set stays put")
	require.Nil(t, got.Get("inside/copy.png"))
}

func TestLoadProvenance_SizeCap(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, MaxProvenanceFileBytes+1)
	require.NoError(t, os.WriteFile(filepath.Join(root, provenance.FileName), big, 0o600))
	_, err := LoadProvenance(root)
	require.ErrorContains(t, err, "larger than")
}
