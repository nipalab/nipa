package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientconfig "github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestSetupStatusCmd_JSON(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "new.txt", "untracked")
	stagePath(t, root, "staged.txt")
	writeFile(t, root, "staged.txt", "ready to push")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupStatusCmd(), "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"branch":"main","staged":["staged.txt"],"deleted":[],"modified":[],"untracked":["new.txt"],"missing":[],"conflicts":[]}`, out)
}

func TestSetupStatusCmd_JSONFromEnv(t *testing.T) {
	root := setupRepo(t, "main")
	t.Setenv(output.EnvFormat, "json")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupStatusCmd())
	require.NoError(t, err)
	require.JSONEq(t, `{"branch":"main","staged":[],"deleted":[],"modified":[],"untracked":[],"missing":[],"conflicts":[]}`, out)
}

func TestDiffCmd_JSON(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--json")
	require.NoError(t, err)

	var parsed output.Diff
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Len(t, parsed.Changes, 1)
	change := parsed.Changes[0]
	require.Equal(t, "a.txt", change.Path)
	require.Equal(t, "M", change.Status)
	require.NotNil(t, change.Old)
	require.NotNil(t, change.New)
	require.Len(t, change.Hunks, 1)
	require.Equal(t, []string{"context", "delete", "add", "context"}, jsonLineKinds(change.Hunks[0].Lines))
}

func TestDiffCmd_JSONAdded(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "new.txt", "hello\n")
	stagePath(t, root, "new.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--json")
	require.NoError(t, err)

	var parsed output.Diff
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Len(t, parsed.Changes, 1)
	require.Equal(t, "A", parsed.Changes[0].Status)
	require.Nil(t, parsed.Changes[0].Old)
	require.NotNil(t, parsed.Changes[0].New)
}

func TestDiffCmd_JSONConflictsWithFormat(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	_, err := runCmdInDir(t, root, newDiffCmd(t), "--json", "--stat")
	require.EqualError(t, err, "--json cannot be combined with another output format")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestDiffCmd_JSONConflictsWithExtDiff(t *testing.T) {
	t.Setenv(clientconfig.EnvDiffExternal, "cat")
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	_, err := runCmdInDir(t, root, newDiffCmd(t), "--json", "--ext-diff")
	require.EqualError(t, err, "--json cannot be combined with --ext-diff")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestSetupLogCmd_JSON(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newLogCli(testEntries(), nil)

	out, err := runLogCmd(t, cli, root, "--json")
	require.NoError(t, err)

	var parsed output.Log
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Len(t, parsed.Commits, 3)
	require.Equal(t, "6y1", parsed.Commits[0].ID)
	require.Equal(t, "Alice", parsed.Commits[0].AuthorName)
	require.Equal(t, "2026-01-01T00:00:00Z", parsed.Commits[0].CreatedAt)
	require.Empty(t, parsed.Commits[0].ParentIDs)
	require.Contains(t, parsed.Commits[1].Message, "with body")
}

func TestSetupBranchCmd_JSON(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli()

	out, err := runCmdInDir(t, root, cli.setupBranchCmd(), "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"branch":"main"}`, out)
}

func TestSetupBranchCmd_ListAllJSON(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli(
		&serverDomain.Branch{Name: "main", IsDefault: true},
		&serverDomain.Branch{Name: "dev", IsProtected: true},
	)

	out, err := runCmdInDir(t, root, cli.setupBranchCmd(), "-a", "--json")
	require.NoError(t, err)

	var parsed output.Branches
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "main", parsed.Current)
	require.Len(t, parsed.Branches, 2)
	require.True(t, parsed.Branches[0].Current)
	require.True(t, parsed.Branches[0].Default)
	require.True(t, parsed.Branches[1].Protected)
	require.False(t, parsed.Branches[1].Current)
}

func TestSetupBranchCmd_JSONConflictsWithCreate(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli()

	_, err := runCmdInDir(t, root, cli.setupBranchCmd(), "--json", "-c", "feature")
	require.EqualError(t, err, "--json cannot be combined with --create or --delete")
}

func TestSetupLockListCmd_JSON(t *testing.T) {
	root := setupRepo(t, "feature")
	mrNumber := int64(3)
	client := &fakeLockClient{listResult: []*domain.FileLock{
		{
			Path:               "art/tex.png",
			Global:             true,
			HeldByName:         "bob",
			MergeRequestNumber: &mrNumber,
			AcquiredAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{Path: "audio/loop.wav", Branch: "feature", HeldByName: "alice"},
	}}
	cli := newLockCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "list", "--json")
	require.NoError(t, err)

	var parsed output.Locks
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Len(t, parsed.Locks, 2)
	require.Equal(t, "mainline", parsed.Locks[0].Scope)
	require.Equal(t, int64(3), *parsed.Locks[0].MergeRequestNumber)
	require.Equal(t, "2026-01-01T00:00:00Z", parsed.Locks[0].AcquiredAt)
	require.Equal(t, "branch", parsed.Locks[1].Scope)
	require.Equal(t, "feature", parsed.Locks[1].Branch)
}

func TestSetupLockListCmd_JSONEmpty(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newLockCli(t, &fakeLockClient{})

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "list", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"locks":[]}`, out)
}

func TestSetupMrListCmd_JSON(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{listResult: []*domain.MergeRequest{
		{
			ID:           "abc",
			Number:       1,
			Status:       domain.MergeRequestOpen,
			SourceBranch: "feature",
			TargetBranch: "main",
			Title:        "Add b",
			CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "list", "--json")
	require.NoError(t, err)

	var parsed output.MergeRequests
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Len(t, parsed.MergeRequests, 1)
	require.Equal(t, "feature", parsed.MergeRequests[0].SourceBranch)
	require.Equal(t, "2026-01-01T00:00:00Z", parsed.MergeRequests[0].CreatedAt)
}

func TestSetupMrListCmd_JSONEmpty(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{})

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "list", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"merge_requests":[]}`, out)
}

func jsonLineKinds(lines []output.DiffLine) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Kind
	}
	return out
}
