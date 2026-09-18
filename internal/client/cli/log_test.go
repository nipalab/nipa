package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeLogRepo struct {
	fakeRepoInterface
	entries []*serverDomain.CommitLogEntry
	err     error
}

func (f *fakeLogRepo) GetCommitLog(_ context.Context, _, _, _ string, _ *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
	return f.entries, f.err
}

func newLogCli(entries []*serverDomain.CommitLogEntry, err error) *Cli {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	repo := usecase.NewRepo(auth, &fakeLogRepo{entries: entries, err: err}, fakeLocalRepo{})
	return NewCli(&fakeUsecaseContainer{auth: auth, repo: repo}, &fakeConnector{})
}

func newLogCliWithConnector(conn *fakeConnector) *Cli {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	repo := usecase.NewRepo(auth, &fakeLogRepo{entries: testEntries()}, fakeLocalRepo{})
	return NewCli(&fakeUsecaseContainer{auth: auth, repo: repo}, conn)
}

func runLogCmd(t *testing.T, cli *Cli, dir string, args ...string) (string, error) {
	t.Helper()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(dir))

	var buf bytes.Buffer
	cmd := cli.setupLogCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

func TestSetupLogCmd_Plain(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newLogCli(testEntries(), nil)

	out, err := runLogCmd(t, cli, root, "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "commit 6y1")
	require.Contains(t, out, "Author: Alice <alice@example.com>")
	require.Contains(t, out, "first commit")
}

func TestSetupLogCmd_Oneline(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newLogCli(testEntries(), nil)

	out, err := runLogCmd(t, cli, root, "--oneline", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "6y1 first commit")
	require.NotContains(t, out, "Author:")
}

func TestSetupLogCmd_Limit(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newLogCli(testEntries(), nil)

	out, err := runLogCmd(t, cli, root, "--no-pager", "-n", "1")
	require.NoError(t, err)
	require.Contains(t, out, "first commit")
	require.NotContains(t, out, "second commit")
}

func TestSetupLogCmd_DefaultBranchConfig(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newLogCli(testEntries(), nil)

	out, err := runLogCmd(t, cli, root, "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "first commit")
}

func TestSetupLogCmd_ServerError(t *testing.T) {
	root := setupRepo(t, "main")
	wantErr := errors.New("log failed")
	cli := newLogCli(nil, wantErr)

	_, err := runLogCmd(t, cli, root, "--no-pager")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupLogCmd_ConnectorError(t *testing.T) {
	root := setupRepo(t, "main")
	wantErr := errors.New("connect failed")
	cli := newLogCliWithConnector(&fakeConnector{err: wantErr})

	_, err := runLogCmd(t, cli, root, "--no-pager")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupLogCmd_InvalidURL(t *testing.T) {
	root := setupRepo(t, "main")
	// overwrite the config with an unparseable URL
	require.NoError(t, os.WriteFile(root+"/.nipa/config", []byte(`{"url":"not-a-url","branch":"main"}`), 0o644))
	cli := newLogCli(testEntries(), nil)

	_, err := runLogCmd(t, cli, root, "--no-pager")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid URL scheme")
}

func TestSetupLogCmd_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newLogCli(testEntries(), nil)

	_, err := runLogCmd(t, cli, dir, "--no-pager")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestIsTTY_NonTerminal(t *testing.T) {
	var buf bytes.Buffer
	require.False(t, isTTY(&buf))

	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer f.Close()
	require.False(t, isTTY(f))
}
