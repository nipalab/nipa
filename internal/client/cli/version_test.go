package cli

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/version"
)

type fakeVersionClient struct {
	host   string
	result *version.Info
	err    error
}

func (f *fakeVersionClient) Connect(_ context.Context, host string) error {
	f.host = host
	return nil
}

func (f *fakeVersionClient) GetServerVersion(context.Context) (*version.Info, error) {
	return f.result, f.err
}

func newVersionCli(client *fakeVersionClient) *Cli {
	return NewCli(&fakeUsecaseContainer{version: usecase.NewVersion(client)}, &fakeConnector{})
}

func pinVersion(t *testing.T, v, revision string) {
	t.Helper()
	prevVersion, prevRevision := version.Version, version.Revision
	t.Cleanup(func() { version.Version, version.Revision = prevVersion, prevRevision })
	version.Version, version.Revision = v, revision
}

func TestSetupVersionCmd_Local(t *testing.T) {
	pinVersion(t, "v1.2.3", "abc1234")
	cli := newVersionCli(&fakeVersionClient{})

	out, err := runCmdInDir(t, t.TempDir(), cli.setupVersionCmd())
	require.NoError(t, err)
	require.Contains(t, out, "nipa version v1.2.3 (revision abc1234")
}

func TestSetupVersionCmd_LocalJSON(t *testing.T) {
	pinVersion(t, "v1.2.3", "abc1234")
	cli := newVersionCli(&fakeVersionClient{})

	out, err := runCmdInDir(t, t.TempDir(), cli.setupVersionCmd(), "--json")
	require.NoError(t, err)

	var parsed version.Info
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "v1.2.3", parsed.Version)
	require.Equal(t, "abc1234", parsed.Revision)
	require.Equal(t, runtime.Version(), parsed.GoVersion)
}

func TestSetupVersionCmd_ServerWithURL(t *testing.T) {
	pinVersion(t, "v1.2.3", "abc1234")
	client := &fakeVersionClient{result: &version.Info{Version: "v1.2.3", Revision: "def5678", GoVersion: runtime.Version()}}
	cli := newVersionCli(client)

	out, err := runCmdInDir(t, t.TempDir(), cli.setupVersionCmd(), "--server", "http://example.com/org/project")
	require.NoError(t, err)
	require.Equal(t, "http://example.com", client.host)
	require.Contains(t, out, "client: v1.2.3 (revision abc1234")
	require.Contains(t, out, "server: v1.2.3 (revision def5678")
	require.NotContains(t, out, "note:")
}

func TestSetupVersionCmd_ServerUsesCloneRemote(t *testing.T) {
	pinVersion(t, "v1.2.3", "abc1234")
	root := setupRepoWithURL(t, "http://nipa.internal/org/project")
	client := &fakeVersionClient{result: &version.Info{Version: "v1.0.0"}}
	cli := newVersionCli(client)

	out, err := runCmdInDir(t, root, cli.setupVersionCmd(), "--server")
	require.NoError(t, err)
	require.Equal(t, "http://nipa.internal", client.host)
	require.Contains(t, out, "server: v1.0.0")
	require.Contains(t, out, "note: client and server versions differ")
}

func TestSetupVersionCmd_ServerJSON(t *testing.T) {
	pinVersion(t, "v1.2.3", "abc1234")
	client := &fakeVersionClient{result: &version.Info{Version: "v1.0.0", Revision: "def5678", GoVersion: runtime.Version()}}
	cli := newVersionCli(client)

	out, err := runCmdInDir(t, t.TempDir(), cli.setupVersionCmd(), "--server", "http://example.com/org/project", "--json")
	require.NoError(t, err)

	var parsed versionPair
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "v1.2.3", parsed.Client.Version)
	require.Equal(t, "v1.0.0", parsed.Server.Version)
	require.Equal(t, "def5678", parsed.Server.Revision)
}

func TestSetupVersionCmd_ServerOutsideRepo(t *testing.T) {
	cli := newVersionCli(&fakeVersionClient{result: &version.Info{Version: "v1.2.3"}})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupVersionCmd(), "--server")
	require.ErrorContains(t, err, "not a nipa repository")
}
