package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/provenance"
)

func TestSetProvenance_CreatesAndStages(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "art/hero.png", "pixels")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	tool, model, note := "  ComfyUI ", "sdxl", "first pass"
	seed := uint64(7)
	edited := true
	prompt := provenance.PromptHash("a dragon")
	out, err := newWorkingCopy(t, local, root).SetProvenance(context.Background(), "/art/hero.png", ProvenanceInput{
		Tool: &tool, Model: &model, Seed: &seed, HumanEdited: &edited, Note: &note, PromptSHA256: &prompt,
	})
	require.NoError(t, err)
	require.True(t, out.Created)
	require.Equal(t, "art/hero.png", out.Path)
	require.Equal(t, []string{provenance.FileName}, local.stageAdd)
	rec := readProvenance(t, root).Get("art/hero.png")
	require.NotNil(t, rec)
	require.Equal(t, "ComfyUI", rec.Tool)
	require.Equal(t, "sdxl", rec.Model)
	require.Equal(t, uint64(7), *rec.Seed)
	require.True(t, rec.HumanEdited)
	require.Equal(t, provenance.SourceManual, rec.Source)
	require.Equal(t, prompt, rec.PromptSHA256)
	require.True(t, strings.HasPrefix(rec.ContentHash, "blake3:"))
	require.False(t, rec.RecordedAt.IsZero())
}

func TestSetProvenance_KeepsUnsetFields(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "hero.png", "pixels")
	f := provenance.New()
	require.NoError(t, f.Set("hero.png", &provenance.Record{Tool: "Firefly", Model: "v3", License: "cc0", Source: provenance.SourceC2PA}))
	require.NoError(t, SaveProvenance(root, f))
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	license := "proprietary"
	out, err := newWorkingCopy(t, local, root).SetProvenance(context.Background(), "hero.png", ProvenanceInput{License: &license})
	require.NoError(t, err)
	require.False(t, out.Created)
	rec := readProvenance(t, root).Get("hero.png")
	require.Equal(t, "Firefly", rec.Tool)
	require.Equal(t, "v3", rec.Model)
	require.Equal(t, "proprietary", rec.License)
	require.Equal(t, provenance.SourceManual, rec.Source)
	require.Nil(t, rec.C2PAVerified)
}

func TestSetProvenance_Errors(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "hero.png", "pixels")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: &domain.Snapshot{}}, root)
	ctx := context.Background()

	_, err := wc.SetProvenance(ctx, "", ProvenanceInput{})
	require.Error(t, err)
	_, err = wc.SetProvenance(ctx, provenance.FileName, ProvenanceInput{})
	require.ErrorContains(t, err, "cannot record")
	_, err = wc.SetProvenance(ctx, "missing.png", ProvenanceInput{})
	require.ErrorContains(t, err, "cannot read missing.png")
	bad := "nothex"
	_, err = wc.SetProvenance(ctx, "hero.png", ProvenanceInput{PromptSHA256: &bad})
	require.ErrorContains(t, err, "invalid prompt_sha256")

	writeRepoFile(t, root, provenance.FileName, "version: 2\n")
	_, err = wc.SetProvenance(ctx, "hero.png", ProvenanceInput{})
	require.ErrorContains(t, err, "unsupported version")
}
