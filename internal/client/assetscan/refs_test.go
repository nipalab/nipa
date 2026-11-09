package assetscan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
)

var (
	texGUID = strings.Repeat("a", 32)
	matGUID = strings.Repeat("b", 32)
)

func matYAML(guids ...string) string {
	s := "%YAML 1.1\n--- !u!21 &2100000\nMaterial:\n"
	for _, g := range guids {
		s += "  m_Tex: {fileID: 2800000, guid: " + g + ", type: 3}\n"
	}
	return s
}

func unityRepo(t *testing.T) string {
	return setupRepo(t, map[string]string{
		"Assets/Art/hero.png":      "png",
		"Assets/Art/hero.png.meta": "guid: " + texGUID + "\n",
		"Assets/Mat/hero.mat":      matYAML(texGUID),
		"Assets/Mat/hero.mat.meta": "guid: " + matGUID + "\n",
		"Assets/Hero.prefab":       matYAML(matGUID, strings.Repeat("c", 32)),
		"notes.txt":                "n\n",
	})
}

func TestRefs(t *testing.T) {
	root := unityRepo(t)
	report, err := Refs(context.Background(), offlineDiff(), root, "Assets/Mat/hero.mat", assetrefs.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Assets/Hero.prefab"}, report.UsedBy)
	require.Len(t, report.Uses, 1)
	assert.Equal(t, "Assets/Art/hero.png", report.Uses[0].Path)
	assert.Empty(t, report.Unknown)
	assert.Equal(t, 2, report.Scanned)

	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main", Sparse: []string{"Assets"}}))
	require.NoError(t, lr.Close())
	report, err = Refs(context.Background(), offlineDiff(), root, "Assets/Hero.prefab", assetrefs.Options{})
	require.NoError(t, err)
	assert.Len(t, report.Uses, 1)
	require.Len(t, report.Unknown, 1)
	assert.Equal(t, "guid:"+strings.Repeat("c", 32), report.Unknown[0].Raw)

	_, err = Refs(context.Background(), offlineDiff(), t.TempDir(), "x", assetrefs.Options{})
	require.Error(t, err)
}

func TestImpact_WorkingCopy(t *testing.T) {
	root := unityRepo(t)
	ctx := context.Background()

	writeFile(t, root, "notes.txt", "changed\n")
	changes, err := Enumerate(ctx, offlineDiff(), root, Request{})
	require.NoError(t, err)
	out, err := Impact(ctx, offlineDiff(), root, changes, assetrefs.Options{})
	require.NoError(t, err)
	assert.Nil(t, out, "no engine asset changed")

	for _, p := range []string{"Assets/Art/hero.png", "Assets/Art/hero.png.meta"} {
		require.NoError(t, os.Remove(filepath.Join(root, filepath.FromSlash(p))))
	}
	changes, err = Enumerate(ctx, offlineDiff(), root, Request{})
	require.NoError(t, err)
	out, err = Impact(ctx, offlineDiff(), root, changes, assetrefs.Options{})
	require.NoError(t, err)
	assert.Equal(t, []assetrefs.BrokenRef{{From: "Assets/Mat/hero.mat", Raw: "guid:" + texGUID, Target: "Assets/Art/hero.png", Reason: assetrefs.ReasonRemoved}}, out.Broken)

	_, err = Impact(ctx, offlineDiff(), t.TempDir(), changes, assetrefs.Options{})
	require.Error(t, err)
}

func TestRelevant(t *testing.T) {
	assert.False(t, Relevant(&Changes{Files: []Change{{Item: ChangeItem("a.txt", "", "M", SideSnapshot, SideWorking)}}}))
	assert.True(t, Relevant(&Changes{Files: []Change{{Item: ChangeItem("b.txt", "Assets/a.png", "R", SideSnapshot, SideWorking)}}}))
}
