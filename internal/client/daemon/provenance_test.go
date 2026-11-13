package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/provenance"
)

const requireArt = `version: 1
rules:
  - name: generated
    paths: ["art/**"]
    require_provenance: true
`

func provenanceServer(t *testing.T, files map[string]string) (*Server, string) {
	t.Helper()
	root := newTreeClone(t, files)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func hashOf(t *testing.T, root, p string) string {
	t.Helper()
	h, err := usecase.HashWorkingFile(root, p)
	require.NoError(t, err)
	return h
}

func TestServer_Provenance_Statuses(t *testing.T) {
	srv, root := provenanceServer(t, map[string]string{
		"art/same.png": "a", "art/changed.png": "b", "art/edited.png": "c", "art/none.png": "d",
		"misc/free.png": "e", "misc/gone.png": "f", ".nipaassets.yml": requireArt,
	})
	seed := uint64(42)
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	f := provenance.New()
	require.NoError(t, f.Set("art/same.png", &provenance.Record{ContentHash: hashOf(t, root, "art/same.png"), Tool: "ComfyUI", Model: "sdxl", Seed: &seed, Source: provenance.SourcePNGText, RecordedAt: at, PromptSHA256: provenance.PromptHash("x")}))
	require.NoError(t, f.Set("art/changed.png", &provenance.Record{ContentHash: hashOf(t, root, "art/same.png"), Tool: "Firefly"}))
	require.NoError(t, f.Set("art/edited.png", &provenance.Record{ContentHash: hashOf(t, root, "art/same.png"), HumanEdited: true}))
	require.NoError(t, f.Set("misc/gone.png", &provenance.Record{ContentHash: hashOf(t, root, "misc/gone.png"), Tool: "Midjourney"}))
	require.NoError(t, usecase.SaveProvenance(root, f))
	require.NoError(t, os.Remove(filepath.Join(root, "misc", "gone.png")))

	res, err := srv.Provenance(context.Background(), &daemonpb.ProvenanceRequest{Root: root, Paths: []string{
		"art/same.png", "art/changed.png", "art/edited.png", "art/none.png", "misc/free.png", "misc/gone.png",
	}})
	require.NoError(t, err)
	assert.True(t, res.GetEnabled())
	got := map[string]*daemonpb.ProvenanceEntry{}
	for _, e := range res.GetEntries() {
		got[e.GetPath()] = e
	}
	require.Len(t, got, 5)
	same := got["art/same.png"]
	assert.Equal(t, provenance.StatusRecorded, same.GetStatus())
	assert.True(t, same.GetRequired())
	assert.True(t, same.GetHasRecord())
	assert.Equal(t, "ComfyUI", same.GetTool())
	assert.Equal(t, "sdxl", same.GetModel())
	assert.Equal(t, uint64(42), same.GetSeed())
	assert.Equal(t, provenance.SourcePNGText, same.GetSource())
	assert.Equal(t, "2026-03-04T05:06:07Z", same.GetRecordedAt())
	assert.Len(t, same.GetPromptSha256(), 64)
	assert.Equal(t, provenance.StatusModified, got["art/changed.png"].GetStatus())
	assert.Equal(t, provenance.StatusEdited, got["art/edited.png"].GetStatus())
	none := got["art/none.png"]
	assert.Equal(t, provenance.StatusMissing, none.GetStatus())
	assert.True(t, none.GetRequired())
	assert.False(t, none.GetHasRecord())
	assert.True(t, none.GetExists())
	gone := got["misc/gone.png"]
	assert.False(t, gone.GetExists())
	assert.False(t, gone.GetRequired())
	assert.Equal(t, provenance.StatusModified, gone.GetStatus())
}

func TestServer_Provenance_NoOptIn(t *testing.T) {
	srv, root := provenanceServer(t, map[string]string{"art/hero.png": "a"})
	res, err := srv.Provenance(context.Background(), &daemonpb.ProvenanceRequest{Root: root, Paths: []string{"art/hero.png"}})
	require.NoError(t, err)
	assert.False(t, res.GetEnabled())
	assert.Empty(t, res.GetEntries())
}

func TestServer_Provenance_RequiredWithoutFile(t *testing.T) {
	srv, root := provenanceServer(t, map[string]string{"art/hero.png": "a", ".nipaassets.yml": requireArt})
	res, err := srv.Provenance(context.Background(), &daemonpb.ProvenanceRequest{Root: root, Paths: []string{"art/hero.png"}})
	require.NoError(t, err)
	assert.False(t, res.GetEnabled())
	require.Len(t, res.GetEntries(), 1)
	assert.Equal(t, provenance.StatusMissing, res.GetEntries()[0].GetStatus())
	assert.True(t, res.GetEntries()[0].GetRequired())
}

func TestServer_Provenance_Errors(t *testing.T) {
	srv, root := provenanceServer(t, map[string]string{"art/hero.png": "a"})
	ctx := context.Background()
	_, err := srv.Provenance(ctx, &daemonpb.ProvenanceRequest{Root: root, Paths: []string{""}})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.Provenance(ctx, &daemonpb.ProvenanceRequest{Root: root, Paths: make([]string, maxProvenancePaths+1)})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipaassets.yml"), []byte("version: 9\n"), 0o644))
	_, err = srv.Provenance(ctx, &daemonpb.ProvenanceRequest{Root: root, Paths: []string{"art/hero.png"}})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	require.NoError(t, os.Remove(filepath.Join(root, ".nipaassets.yml")))

	require.NoError(t, os.WriteFile(filepath.Join(root, provenance.FileName), []byte("version: 3\n"), 0o644))
	_, err = srv.Provenance(ctx, &daemonpb.ProvenanceRequest{Root: root, Paths: []string{"art/hero.png"}})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	ctxDone, cancel := context.WithCancel(ctx)
	cancel()
	_, err = srv.Provenance(ctxDone, &daemonpb.ProvenanceRequest{Root: root, Paths: []string{"art/hero.png"}})
	require.Error(t, err)
}

func TestServer_SetProvenance(t *testing.T) {
	srv, root := provenanceServer(t, map[string]string{"art/hero.png": "a", ".nipaassets.yml": requireArt})
	rp, unref, err := srv.repos.ref(root)
	require.NoError(t, err)
	unref()
	sub, unsubscribe := rp.changes.subscribe()
	defer unsubscribe()

	tool, model := "ComfyUI", "sdxl"
	seed := uint64(9)
	prompt := provenance.PromptHash("a dragon")
	res, err := srv.SetProvenance(context.Background(), &daemonpb.SetProvenanceRequest{
		Root: root, Path: "art/hero.png", Tool: &tool, Model: &model, Seed: &seed, PromptSha256: &prompt,
	})
	require.NoError(t, err)
	assert.True(t, res.GetCreated())
	e := res.GetEntry()
	assert.Equal(t, provenance.StatusRecorded, e.GetStatus())
	assert.True(t, e.GetRequired())
	assert.Equal(t, provenance.SourceManual, e.GetSource())
	assert.Equal(t, uint64(9), e.GetSeed())
	assert.NotEmpty(t, e.GetRecordedAt())

	f, err := usecase.LoadProvenance(root)
	require.NoError(t, err)
	require.NotNil(t, f.Get("art/hero.png"))
	staged, err := rp.localRepo.ListStaged()
	require.NoError(t, err)
	assert.Contains(t, staged, provenance.FileName)
	change := sub.take()
	require.NotNil(t, change)
	assert.Equal(t, []string{provenance.FileName}, change.GetPaths())

	again, err := srv.Provenance(context.Background(), &daemonpb.ProvenanceRequest{Root: root, Paths: []string{"art/hero.png"}})
	require.NoError(t, err)
	assert.True(t, again.GetEnabled())
	assert.Equal(t, provenance.StatusRecorded, again.GetEntries()[0].GetStatus())

	res, err = srv.SetProvenance(context.Background(), &daemonpb.SetProvenanceRequest{Root: root, Path: "art/hero.png"})
	require.NoError(t, err)
	assert.False(t, res.GetCreated())
	assert.Equal(t, "ComfyUI", res.GetEntry().GetTool())
}

func TestServer_SetProvenance_Errors(t *testing.T) {
	srv, root := provenanceServer(t, map[string]string{"art/hero.png": "a"})
	_, err := srv.SetProvenance(context.Background(), &daemonpb.SetProvenanceRequest{Root: root, Path: "art/missing.png"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.True(t, strings.Contains(err.Error(), "cannot read"))
	bad := "zz"
	_, err = srv.SetProvenance(context.Background(), &daemonpb.SetProvenanceRequest{Root: root, Path: "art/hero.png", PromptSha256: &bad})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.SetProvenance(context.Background(), &daemonpb.SetProvenanceRequest{Root: t.TempDir(), Path: "art/hero.png"})
	require.Error(t, err)
}
