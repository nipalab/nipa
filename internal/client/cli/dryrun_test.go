package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestDryRunRequested(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	addDryRunFlags(cmd)

	dryRun, err := dryRunRequested(cmd)
	require.NoError(t, err)
	require.False(t, dryRun)

	require.NoError(t, cmd.Flags().Set("json", "true"))
	_, err = dryRunRequested(cmd)
	require.EqualError(t, err, "--json requires --dry-run")

	require.NoError(t, cmd.Flags().Set("dry-run", "true"))
	dryRun, err = dryRunRequested(cmd)
	require.NoError(t, err)
	require.True(t, dryRun)
}

func TestDryRunRequested_EnvJSONIgnored(t *testing.T) {
	t.Setenv(output.EnvFormat, "json")
	cmd := &cobra.Command{Use: "x"}
	addDryRunFlags(cmd)

	dryRun, err := dryRunRequested(cmd)
	require.NoError(t, err)
	require.False(t, dryRun)
}

func TestWritePlan_Human(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	plan := &domain.Plan{
		Kind:      "revert",
		Conflicts: []string{"c.txt"},
		Changes: []domain.PlanChange{
			{Path: "a.txt", Status: "M"},
			{Path: "b.txt", Status: "D"},
		},
	}
	require.NoError(t, writePlan(cmd, plan))
	require.Equal(t, "M  a.txt\nD  b.txt\nC  c.txt\nwould revert 2 file(s), 1 conflict(s)\n", buf.String())
}

func TestWritePlan_UpToDate(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	require.NoError(t, writePlan(cmd, &domain.Plan{Kind: "merge", UpToDate: true}))
	require.Equal(t, "up to date\n", buf.String())
}

func TestSetupPushCmd_DryRun(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "hello world")
	stagePath(t, root, "a.txt")

	client := &fakePushClient{}
	cli := newPushCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupPushCmd(), "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "A  a.txt")
	require.Contains(t, out, "would push 1 file(s)")
	require.Empty(t, client.host, "a dry run must not connect to the server")
	require.Equal(t, []string{"a.txt"}, stagedPathsAt(t, root), "a dry run must keep staged files")
}

func TestSetupPushCmd_DryRunJSON(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "hello world")
	stagePath(t, root, "a.txt")
	cli := newPushCli(t, &fakePushClient{})

	out, err := runCmdInDir(t, root, cli.setupPushCmd(), "--dry-run", "--json")
	require.NoError(t, err)

	var plan output.Plan
	require.NoError(t, json.Unmarshal([]byte(out), &plan))
	require.Equal(t, "push", plan.Kind)
	require.Equal(t, []output.PlanChange{{Path: "a.txt", Status: "A", SizeBytes: 11}}, plan.Changes)
	require.Greater(t, plan.UploadObjects, 0)
	require.Greater(t, plan.UploadBytes, int64(0))
}

func TestSetupPushCmd_DryRunNothingStaged(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPushCli(t, &fakePushClient{})

	_, err := runCmdInDir(t, root, cli.setupPushCmd(), "--dry-run")
	require.EqualError(t, err, "nothing staged to push; run nipa add first")
}

func TestSetupPushCmd_JSONRequiresDryRun(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newPushCli(t, &fakePushClient{})

	_, err := runCmdInDir(t, root, cli.setupPushCmd(), "--json", "-m", "m")
	require.EqualError(t, err, "--json requires --dry-run")
}

func TestSetupMergeCmd_DryRunUpToDate(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeMergeClient{baseInfo: &domain.MergeBaseInfo{
		SourceCommitID:    "F1",
		MergeBaseCommitID: "F1",
	}}
	cli := newMergeCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature", "--dry-run")
	require.NoError(t, err)
	require.Equal(t, "up to date\n", out)
	require.False(t, client.pushCalled)
}

func TestSetupMergeCmd_DryRunFastForward(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeMergeClient{
		baseInfo: &domain.MergeBaseInfo{
			TargetCommitID:    "T1",
			SourceCommitID:    "F1",
			SourceCommitHash:  "src-hash",
			MergeBaseCommitID: "T1",
		},
		treeByBranch: map[string]*serverDomain.TreeNode{
			"main": {FileChildren: []*serverDomain.File{
				{Name: "a.txt", Mode: 2, SizeBytes: 3, Chunks: chunksOf(t, "old")},
			}},
			"feature": {FileChildren: []*serverDomain.File{
				{Name: "a.txt", Mode: 2, SizeBytes: 3, Chunks: chunksOf(t, "new")},
				{Name: "b.txt", Mode: 2, SizeBytes: 1, Chunks: chunksOf(t, "b")},
			}},
		},
	}
	cli := newMergeCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "fast-forward feature")
	require.Contains(t, out, "M  a.txt")
	require.Contains(t, out, "A  b.txt")
	require.False(t, client.pushCalled, "a dry run must not commit the merge")
}

func TestSetupMergeCmd_DryRunJSON(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeMergeClient{baseInfo: &domain.MergeBaseInfo{
		SourceCommitID:    "F1",
		MergeBaseCommitID: "F1",
	}}
	cli := newMergeCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature", "--dry-run", "--json")
	require.NoError(t, err)

	var plan output.Plan
	require.NoError(t, json.Unmarshal([]byte(out), &plan))
	require.Equal(t, "merge", plan.Kind)
	require.True(t, plan.UpToDate)
	require.Empty(t, plan.Changes)
}

func TestSetupRevertCmd_DryRunFlagConflicts(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd(), "--dry-run", "--abort")
	require.EqualError(t, err, "--dry-run cannot be combined with --continue, --abort or --skip")

	_, err = runCmdInDir(t, root, cli.setupRevertCmd(), "--dry-run", "--no-commit", "2")
	require.EqualError(t, err, "--dry-run cannot be combined with --no-commit")
}

func TestWritePlan_Nil(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	require.EqualError(t, writePlan(cmd, nil), "nothing to report")
}

func TestWritePlan_MergeSummary(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	plan := &domain.Plan{
		Kind:         "merge",
		SourceBranch: "feature",
		Conflicts:    []string{"c.txt"},
		Changes:      []domain.PlanChange{{Path: "a.txt", Status: "M"}},
	}
	require.NoError(t, writePlan(cmd, plan))
	require.Equal(t, "M  a.txt\nC  c.txt\nwould merge 1 file(s), 1 conflict(s)\n", buf.String())
}

func TestSetupMergeCmd_JSONRequiresDryRun(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newMergeCli(t, &fakeMergeClient{})

	_, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature", "--json")
	require.EqualError(t, err, "--json requires --dry-run")
}

func TestSetupRevertCmd_JSONRequiresDryRun(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd(), "2", "--json")
	require.EqualError(t, err, "--json requires --dry-run")
}

func TestSetupRevertCmd_DryRun(t *testing.T) {
	head := revertCliTree(t, 0xaa, map[string]string{"a.txt": "v2\n"})
	v1 := revertCliTree(t, 0x01, map[string]string{"a.txt": "v1\n"})
	v2 := revertCliTree(t, 0x02, map[string]string{"a.txt": "v2\n"})
	root := setupRevertRepo(t, head, "v2\n", "v1\n")
	client := &fakeRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: v2},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: v1},
		},
		headTree: head,
	}
	cli := newRevertCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupRevertCmd(), "2", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "M  a.txt")
	require.Contains(t, out, "would revert 1 file(s), 0 conflict(s)")
	require.False(t, client.pushCalled)
}

func TestSetupMergeCmd_DryRunAbortRejected(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newMergeCli(t, &fakeMergeClient{})

	_, err := runCmdInDir(t, root, cli.setupMergeCmd(), "--abort", "--dry-run")
	require.EqualError(t, err, "--dry-run cannot be combined with --abort")
}
