package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/provenance"
)

func provenanceFixture(t *testing.T) string {
	t.Helper()
	root := setupRepo(t, map[string]string{"a.txt": "one\n", "art_a.png": "aaa", "art_b.png": "bbb", "gone.png": "g", "zz_gone.png": "z"})
	for _, p := range []string{"gone.png", "zz_gone.png"} {
		require.NoError(t, os.Remove(filepath.Join(root, p)))
	}
	f := provenance.New()
	require.NoError(t, f.Set("art_a.png", &provenance.Record{Tool: "ComfyUI", ContentHash: provenance.HashBytes([]byte("aaa"))}))
	require.NoError(t, f.Set("gone.png", &provenance.Record{Tool: "Firefly"}))
	require.NoError(t, f.Set("zz_gone.png", &provenance.Record{Tool: "Firefly"}))
	require.NoError(t, usecase.SaveProvenance(root, f))
	writeFile(t, root, assetbudget.FileName, "rules:\n  - paths: [\"*.png\"]\n    require_provenance: true\n")
	return root
}

func TestProvenanceTool(t *testing.T) {
	root := provenanceFixture(t)
	session := connect(t, Options{NewUseCases: offlineUseCases})

	out := decode[provenanceOutput](t, call(t, session, "nipa_provenance", map[string]any{"repo": root}))
	require.True(t, out.Config)
	require.Len(t, out.Records, 1)
	require.Equal(t, "art_a.png", out.Records[0].Path)
	require.Equal(t, provenance.StatusRecorded, out.Records[0].Status)
	require.Equal(t, "ComfyUI", out.Records[0].Record.Tool)
	require.Equal(t, []string{"art_b.png"}, out.Missing)
	require.Equal(t, []string{"gone.png", "zz_gone.png"}, out.Orphaned)
	require.False(t, out.Truncated)

	capped := decode[provenanceOutput](t, call(t, session, "nipa_provenance", map[string]any{"repo": root, "max_files": 1}))
	require.True(t, capped.Truncated)
	require.Len(t, capped.Records, 1)
	require.Empty(t, capped.Missing)
	require.Equal(t, []string{"gone.png"}, capped.Orphaned)

	filtered := decode[provenanceOutput](t, call(t, session, "nipa_provenance", map[string]any{"repo": root, "paths": []string{"art_b.png"}}))
	require.Empty(t, filtered.Records)
	require.Equal(t, []string{"art_b.png"}, filtered.Missing)
	require.Empty(t, filtered.Orphaned)
}

func TestProvenanceTool_Since(t *testing.T) {
	root := provenanceFixture(t)
	writeFile(t, root, "new.png", "new")
	stageFile(t, root, "new.png")
	session := connect(t, Options{NewUseCases: diffUseCases(revisionClient())})
	out := decode[provenanceOutput](t, call(t, session, "nipa_provenance", map[string]any{"repo": root, "since": "main"}))
	require.Contains(t, out.Missing, "new.png")
	require.NotContains(t, out.Missing, "a.txt")

	res := call(t, session, "nipa_provenance", map[string]any{"repo": root, "since": "nope"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), `revision "nope" not found`)

	unconfigured := connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }})
	res = call(t, unconfigured, "nipa_provenance", map[string]any{"repo": root, "since": "main"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")
	ok := decode[provenanceOutput](t, call(t, unconfigured, "nipa_provenance", map[string]any{"repo": root}))
	require.True(t, ok.Config)
}

func TestProvenanceTool_Errors(t *testing.T) {
	session := connect(t, Options{NewUseCases: offlineUseCases})
	res := call(t, session, "nipa_provenance", map[string]any{"repo": filepath.Join(t.TempDir(), "missing")})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "not a nipa repository")

	root := provenanceFixture(t)
	writeFile(t, root, provenance.FileName, "version: 5\n")
	res = call(t, session, "nipa_provenance", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "unsupported version 5")

	empty := setupRepo(t, map[string]string{"a.txt": "x"})
	out := decode[provenanceOutput](t, call(t, session, "nipa_provenance", map[string]any{"repo": empty}))
	require.False(t, out.Config)
	require.NotNil(t, out.Records)
	require.NotNil(t, out.Missing)
	require.NotNil(t, out.Orphaned)
}
