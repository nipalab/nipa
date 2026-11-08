package cli

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
)

const testBudget = `rules:
  - name: textures
    paths: ["*.png"]
    power_of_two: true
  - name: size
    paths: ["big/"]
    max_bytes: 4
`

func testPNG(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, w, h))))
	return buf.String()
}

func newAssetsCmd() *cobra.Command {
	cli := NewCli(&fakeUsecaseContainer{diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())}, &fakeConnector{})
	return cli.setupAssetsCmd()
}

func TestAssetsCheck_NoConfig(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})

	out, err := runCmdInDir(t, root, newAssetsCmd(), "check")
	require.NoError(t, err)
	require.Equal(t, "no .nipaassets.yml\n", out)

	out, err = runCmdInDir(t, root, newAssetsCmd(), "check", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"config":false,"violations":[],"checked":0}`, out)
}

func TestAssetsCheck_Violations(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"tex.png": testPNG(t, 4, 4)})
	writeFile(t, root, ".nipaassets.yml", testBudget)
	writeFile(t, root, "tex.png", testPNG(t, 6, 4))
	writeFile(t, root, "big/blob.bin", "0123456789")
	writeFile(t, root, "broken.png", "nope")
	stagePath(t, root, "big/blob.bin")
	stagePath(t, root, "broken.png")

	out, err := runCmdInDir(t, root, newAssetsCmd(), "check")
	require.ErrorIs(t, err, ErrBudgetViolations)
	require.Equal(t, 2, ExitCode(err))
	require.Contains(t, out, "big/blob.bin: max_bytes: 10 B exceeds 4 B (rule size)\n")
	require.Contains(t, out, "tex.png: power_of_two: 6x4 is not a power of two (rule textures)\n")
	require.Contains(t, out, "skipped broken.png: ")
	require.Contains(t, out, "2 violation(s) in 3 checked file(s)\n")

	out, err = runCmdInDir(t, root, newAssetsCmd(), "check", "--json", "--", "tex.png")
	require.ErrorIs(t, err, ErrBudgetViolations)
	var report assetscan.Report
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.True(t, report.Config)
	require.Equal(t, 1, report.Checked)
	require.Len(t, report.Violations, 1)
	require.Equal(t, "power_of_two", report.Violations[0].Limit)

	out, err = runCmdInDir(t, root, newAssetsCmd(), "check", "--staged", "--", "big")
	require.ErrorIs(t, err, ErrBudgetViolations)
	require.NotContains(t, out, "tex.png")

	writeFile(t, root, "tex.png", testPNG(t, 8, 4))
	out, err = runCmdInDir(t, root, newAssetsCmd(), "check", "--", "tex.png")
	require.NoError(t, err)
	require.Equal(t, "0 violation(s) in 1 checked file(s)\n", out)
}

func TestAssetsCheck_ArgumentErrors(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, ".nipaassets.yml", testBudget)
	for _, args := range [][]string{
		{"check", "a", "b", "c"},
		{"check", "a.."},
		{"check", "--staged", "main"},
	} {
		_, err := runCmdInDir(t, root, newAssetsCmd(), args...)
		require.Error(t, err, args)
	}
	writeFile(t, root, ".nipaassets.yml", "nope: 1\n")
	_, err := runCmdInDir(t, root, newAssetsCmd(), "check")
	require.ErrorContains(t, err, "field nope not found")

	_, err = runCmdInDir(t, t.TempDir(), newAssetsCmd(), "check")
	require.Error(t, err)
}

func newPushDiffCli(t *testing.T) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{
		push: usecase.NewPush(auth, &fakePushClient{}, localrepo.NewLocalRepo()),
		diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo()),
	}, &fakeConnector{})
}

func TestSetupPushCmd_DryRunAssetViolations(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "tex.png", testPNG(t, 6, 4))
	writeFile(t, root, "ok.txt", "fine")
	stagePath(t, root, "tex.png")
	stagePath(t, root, "ok.txt")

	out, err := runCmdInDir(t, root, newPushDiffCli(t).setupPushCmd(), "--dry-run", "--json")
	require.NoError(t, err)
	require.NotContains(t, out, "asset_violations")

	writeFile(t, root, ".nipaassets.yml", testBudget)
	out, err = runCmdInDir(t, root, newPushDiffCli(t).setupPushCmd(), "--dry-run", "--json")
	require.NoError(t, err)
	var plan output.Plan
	require.NoError(t, json.Unmarshal([]byte(out), &plan))
	require.Len(t, plan.AssetViolations, 1)
	require.Equal(t, "tex.png", plan.AssetViolations[0].Path)

	out, err = runCmdInDir(t, root, newPushDiffCli(t).setupPushCmd(), "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "warning: 1 asset budget violation(s)")
	require.Contains(t, out, "  tex.png: power_of_two: 6x4 is not a power of two (rule textures)\n")

	writeFile(t, root, ".nipaassets.yml", "rules: 3\n")
	_, err = runCmdInDir(t, root, newPushDiffCli(t).setupPushCmd(), "--dry-run")
	require.Error(t, err)
}

func TestPushBudget_NoPlanOrDiff(t *testing.T) {
	c := NewCli(&fakeUsecaseContainer{}, &fakeConnector{})
	v, err := c.pushBudget(&cobra.Command{}, t.TempDir(), &domain.Plan{})
	require.NoError(t, err)
	require.Nil(t, v)
}
