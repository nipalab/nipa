package assetscan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/domain"
)

func TestPlanChanges(t *testing.T) {
	changes := PlanChanges(&domain.Plan{Changes: []domain.PlanChange{
		{Path: "a.png", Status: "A", SizeBytes: 3},
		{Path: "b.png", Status: "D"},
	}})
	assert.Equal(t, SideSnapshot, changes.Old)
	assert.Equal(t, SideWorking, changes.New)
	require.Len(t, changes.Files, 2)
	assert.Equal(t, int64(3), changes.Files[0].NewSize)
	assert.Nil(t, changes.Files[0].Item.Old)
	assert.Nil(t, changes.Files[1].Item.New)
}

func TestPushReport_NilInputs(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, &PushChecks{}, PushReport(ctx, nil, t.TempDir(), &domain.Plan{}))
	assert.Equal(t, &PushChecks{}, PushReport(ctx, offlineDiff(), t.TempDir(), nil))
}

func TestPushReport_NoConfigNoEngineAssets(t *testing.T) {
	root := setupRepo(t, map[string]string{"ok.txt": "a\n"})
	writeFile(t, root, "ok.txt", "b\n")
	out := PushReport(context.Background(), offlineDiff(), root, &domain.Plan{Changes: []domain.PlanChange{{Path: "ok.txt", Status: "M"}}})
	assert.Equal(t, &PushChecks{}, out)
}

func TestPushReport_Violations(t *testing.T) {
	root := setupRepo(t, map[string]string{"tex.png": string(pngBytes(t, 4, 4))})
	writeFile(t, root, "tex.png", string(pngBytes(t, 6, 4)))
	writeFile(t, root, ".nipaassets.yml", budget)
	plan := &domain.Plan{Changes: []domain.PlanChange{{Path: "tex.png", Status: "M"}}}
	out := PushReport(context.Background(), offlineDiff(), root, plan)
	require.NoError(t, out.BudgetErr)
	require.NoError(t, out.ImpactErr)
	require.Len(t, out.Violations, 1)
	assert.Equal(t, "power_of_two", out.Violations[0].Limit)
	assert.Nil(t, out.Impact)

	writeFile(t, root, ".nipaassets.yml", "rules: 3\n")
	out = PushReport(context.Background(), offlineDiff(), root, plan)
	require.Error(t, out.BudgetErr)
	assert.Nil(t, out.Violations)
}

func TestPushReport_Impact(t *testing.T) {
	root := unityRepo(t)
	for _, p := range []string{"Assets/Art/hero.png", "Assets/Art/hero.png.meta"} {
		require.NoError(t, os.Remove(filepath.Join(root, filepath.FromSlash(p))))
	}
	plan := &domain.Plan{Changes: []domain.PlanChange{
		{Path: "Assets/Art/hero.png", Status: "D"},
		{Path: "Assets/Art/hero.png.meta", Status: "D"},
	}}
	out := PushReport(context.Background(), offlineDiff(), root, plan)
	require.NoError(t, out.ImpactErr)
	require.NotNil(t, out.Impact)
	assert.Equal(t, []assetrefs.BrokenRef{{From: "Assets/Mat/hero.mat", Raw: "guid:" + texGUID, Target: "Assets/Art/hero.png", Reason: assetrefs.ReasonRemoved}}, out.Impact.Broken)

	out = PushReport(context.Background(), offlineDiff(), filepath.Join(t.TempDir(), "missing"), plan)
	require.Error(t, out.ImpactErr)
	assert.Nil(t, out.Impact)
}
