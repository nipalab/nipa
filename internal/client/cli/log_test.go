package cli

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeLogRepo struct {
	fakeRepoInterface
	entries []*serverDomain.CommitLogEntry
	err     error
	start   *snow.ID
}

func (f *fakeLogRepo) GetCommitLog(_ context.Context, _, _, _ string, start *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
	f.start = start
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

func runInDir(t *testing.T, dir string, fn func() error) error {
	t.Helper()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(dir))
	return fn()
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

func TestSetupLogCmd_DetachedWalksFromPin(t *testing.T) {
	root := setupRepo(t, "main")
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveCommit(snow.ID(42).Base36(), "hash"))
	require.NoError(t, lr.SaveConfig(domain.Config{
		Url: "http://example.com/org/project", Branch: "main",
		Head: &domain.HeadRef{Kind: domain.HeadKindTag, Name: "v1.0.0"},
	}))
	require.NoError(t, lr.Close())

	fake := &fakeLogRepo{entries: testEntries()}
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	repo := usecase.NewRepo(auth, fake, fakeLocalRepo{})
	cli := NewCli(&fakeUsecaseContainer{auth: auth, repo: repo}, &fakeConnector{})

	out, err := runLogCmd(t, cli, root, "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "first commit")
	require.NotNil(t, fake.start, "a detached log must walk from the pinned commit")
	require.Equal(t, snow.ID(42), *fake.start)
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

func TestDetachedLogStart_NoPinnedCommit(t *testing.T) {
	root := setupRepo(t, "main")

	var got *snow.ID
	err := runInDir(t, root, func() error {
		id, err := detachedLogStart()
		got = id
		return err
	})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestDetachedLogStart_UnparsablePin(t *testing.T) {
	root := setupRepoWithCommit(t, "main", "not-base36*", "hash")

	var got *snow.ID
	err := runInDir(t, root, func() error {
		id, err := detachedLogStart()
		got = id
		return err
	})
	require.NoError(t, err)
	require.Nil(t, got, "an unparsable pin falls back to the branch head")
}

func TestDetachedLogStart_InitError(t *testing.T) {
	root := setupRepo(t, "main")
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".nipa", "objects")))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipa", "objects"), []byte("file"), 0o644))

	err := runInDir(t, root, func() error {
		_, err := detachedLogStart()
		return err
	})
	require.Error(t, err)
}

func TestDetachedLogStart_LoadCommitError(t *testing.T) {
	root := setupRepo(t, "main")
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.Close())

	db, err := sql.Open("sqlite", filepath.Join(root, ".nipa", "nipa.db"))
	require.NoError(t, err)
	_, err = db.Exec("DROP TABLE meta")
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE meta (key TEXT PRIMARY KEY)")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = runInDir(t, root, func() error {
		_, err := detachedLogStart()
		return err
	})
	require.Error(t, err, "a meta table without the value column must surface as an error")
}

func TestDetachedLogStart_NotARepo(t *testing.T) {
	err := runInDir(t, t.TempDir(), func() error {
		_, err := detachedLogStart()
		return err
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a nipa repository")
}

func TestFetchLog_DetachedStartError(t *testing.T) {
	cli := newLogCli(testEntries(), nil)
	cfg := &domain.Config{
		Url: "http://example.com/org/project", Branch: "main",
		Head: &domain.HeadRef{Kind: domain.HeadKindTag, Name: "v1.0.0"},
	}

	err := runInDir(t, t.TempDir(), func() error {
		_, err := cli.fetchLog(nil, cfg, 0)
		return err
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a nipa repository")
}

func TestIsTTY_NonTerminal(t *testing.T) {
	var buf bytes.Buffer
	require.False(t, isTTY(&buf))

	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer f.Close()
	require.False(t, isTTY(f))
}
