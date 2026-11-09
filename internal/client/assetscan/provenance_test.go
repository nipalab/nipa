package assetscan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/provenance"
)

func provenanceRepo(t *testing.T) (string, *usecase.WorkingCopy) {
	t.Helper()
	root := setupRepo(t, map[string]string{"art/a.png": "aaa", "art/b.png": "bbb", "docs/readme.md": "x"})
	f := provenance.New()
	require.NoError(t, f.Set("art/a.png", &provenance.Record{Tool: "ComfyUI", Model: "sdxl", ContentHash: provenance.HashBytes([]byte("aaa"))}))
	require.NoError(t, f.Set("art/c.png", &provenance.Record{Tool: "ComfyUI", ContentHash: provenance.HashBytes([]byte("ccc")), HumanEdited: true}))
	require.NoError(t, f.Set("art/gone.png", &provenance.Record{Tool: "Firefly"}))
	require.NoError(t, usecase.SaveProvenance(root, f))
	writeFile(t, root, "art/c.png", "edited")
	writeFile(t, root, assetbudget.FileName, "rules:\n  - paths: [\"art/**\"]\n    require_provenance: true\n")
	stage(t, root, "art/c.png")
	lr := localrepo.NewLocalRepo()
	wc, err := usecase.NewWorkingCopy(lr, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lr.Close() })
	return root, wc
}

func TestScanProvenance_All(t *testing.T) {
	root, wc := provenanceRepo(t)
	report, err := ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{})
	require.NoError(t, err)
	require.True(t, report.Config)
	got := map[string]string{}
	for _, f := range report.Files {
		got[f.Path] = f.Status
	}
	require.Equal(t, map[string]string{"art/a.png": "recorded", "art/b.png": "missing", "art/c.png": "edited"}, got)
	require.Equal(t, []string{"art/gone.png"}, report.Orphaned)
	require.Equal(t, ProvenanceTotals{Files: 3, Recorded: 1, Edited: 1, Missing: 1, ByTool: map[string]int{"ComfyUI": 2}, ByModel: map[string]int{"sdxl": 1}}, report.Totals)

	writeFile(t, root, "art/a.png", "changed")
	filtered, err := ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{Paths: []string{"art/a.png", "docs"}})
	require.NoError(t, err)
	require.Len(t, filtered.Files, 1)
	require.Equal(t, provenance.StatusModified, filtered.Files[0].Status)
	require.Empty(t, filtered.Orphaned)

	all, err := ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{Paths: []string{"."}})
	require.NoError(t, err)
	require.Len(t, all.Files, 3)

	_, err = ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{Paths: []string{"../x"}})
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ScanProvenance(ctx, nil, wc, root, ProvenanceRequest{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestScanProvenance_NoFileAndErrors(t *testing.T) {
	root, wc := provenanceRepo(t)
	require.NoError(t, os.Remove(filepath.Join(root, provenance.FileName)))
	report, err := ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{})
	require.NoError(t, err)
	require.False(t, report.Config)
	require.Equal(t, 3, report.Totals.Missing)

	writeFile(t, root, assetbudget.FileName, "nope: 1\n")
	_, err = ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{})
	require.ErrorContains(t, err, "nope")

	writeFile(t, root, provenance.FileName, "version: 7\n")
	_, err = ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{})
	require.ErrorContains(t, err, "unsupported version 7")

	require.NoError(t, os.Remove(filepath.Join(root, provenance.FileName)))
	require.NoError(t, os.Mkdir(filepath.Join(root, provenance.FileName), 0o755))
	writeFile(t, root, assetbudget.FileName, "rules: []\n")
	_, err = ScanProvenance(context.Background(), nil, wc, root, ProvenanceRequest{})
	require.Error(t, err)
}

func TestScanProvenance_Since(t *testing.T) {
	root, wc := provenanceRepo(t)
	writeFile(t, root, "art/new.png", "new")
	stage(t, root, "art/new.png")
	client := &fakeClient{
		branches: map[string]string{"v1": "1"},
		commits:  map[string]map[string]string{"1": {"art/a.png": "aaa", "art/b.png": "bbb", "docs/readme.md": "x", "art/old.png": "o"}},
	}
	d := usecase.NewDiff(usecase.NewAuth(fakeLogin{}, fakeStorage{}, nil), client, localrepo.NewLocalRepo())
	report, err := ScanProvenance(context.Background(), d, wc, root, ProvenanceRequest{Since: "nope"})
	require.Error(t, err)
	require.Nil(t, report)

	report, err = ScanProvenance(context.Background(), d, wc, root, ProvenanceRequest{Since: "v1"})
	require.NoError(t, err)
	var paths []string
	for _, f := range report.Files {
		paths = append(paths, f.Path)
	}
	require.Equal(t, []string{"art/c.png", "art/new.png"}, paths)
	require.Equal(t, "v1", report.Since)
}
