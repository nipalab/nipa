package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeBlame struct {
	res      *usecase.BlameResult
	err      error
	root     string
	path     string
	revision string
}

func (f *fakeBlame) Run(_ context.Context, root, path, rev string, _ usecase.BlameProgress) (*usecase.BlameResult, error) {
	f.root, f.path, f.revision = root, path, rev
	return f.res, f.err
}

func runBlameCmd(t *testing.T, blame *fakeBlame, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(dir))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	require.NoError(t, lr.Close())
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(dir))

	cmd := NewCli(&fakeUsecaseContainer{blame: blame}, &fakeConnector{}).setupBlameCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func sampleBlame() *usecase.BlameResult {
	when := time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local)
	return &usecase.BlameResult{
		Path: "src/a.txt",
		Lines: []usecase.BlameLine{
			{Text: "first", CommitID: "abc123"},
			{Text: "second", Uncommitted: true},
			{Text: "third"},
		},
		Commits:   []usecase.BlameCommit{{ID: "abc123", Hash: "ff", AuthorName: "Ana", AuthorEmail: "ana@example.com", Message: "Add a\n", CreatedAt: when}},
		Truncated: true,
	}
}

func TestBlameCmd_Text(t *testing.T) {
	blame := &fakeBlame{res: sampleBlame()}
	out, err := runBlameCmd(t, blame, "src/a.txt", "-r", "v1")
	require.NoError(t, err)
	require.Equal(t, "src/a.txt", blame.path)
	require.Equal(t, "v1", blame.revision)
	require.NotEmpty(t, blame.root)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.Len(t, lines, 4)
	require.Equal(t, "abc123     (Ana 2026-03-04 1) first", lines[0])
	require.Equal(t, "not pushed (               2) second", lines[1])
	require.Equal(t, "?          (               3) third", lines[2])
	require.Contains(t, lines[3], "older than the history")
}

func TestBlameCmd_JSON(t *testing.T) {
	out, err := runBlameCmd(t, &fakeBlame{res: sampleBlame()}, "src/a.txt", "--json")
	require.NoError(t, err)
	var got blameJSONResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, "src/a.txt", got.Path)
	require.Len(t, got.Lines, 3)
	require.Equal(t, 2, got.Lines[1].Line)
	require.True(t, got.Lines[1].Uncommitted)
	require.Equal(t, "Add a", got.Commits[0].Message)
	require.True(t, got.Truncated)
}

func TestBlameCmd_Errors(t *testing.T) {
	boom := errors.New("boom")
	_, err := runBlameCmd(t, &fakeBlame{err: boom}, "a.txt")
	require.ErrorIs(t, err, boom)

	_, err = runBlameCmd(t, &fakeBlame{}, "a.txt", "b.txt")
	require.Error(t, err)

	cmd := NewCli(&fakeUsecaseContainer{blame: &fakeBlame{}}, &fakeConnector{}).setupBlameCmd()
	oldWD, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWD) }()
	require.NoError(t, os.Chdir(t.TempDir()))
	cmd.SetArgs([]string{"a.txt"})
	require.Error(t, cmd.Execute())
}

func TestWriteBlame_Plain(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeBlame(&out, &usecase.BlameResult{Lines: []usecase.BlameLine{{Text: "x", Uncommitted: true}}}))
	require.Equal(t, "not pushed (             1) x\n", out.String())
	require.Error(t, writeBlame(failingWriter{}, sampleBlame()))
}
