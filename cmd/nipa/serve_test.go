package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/sshkey"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeLoginExecutor struct{}

func (fakeLoginExecutor) LoginWithUsernamePassword(context.Context, string, string, string) (*domain.LoginResult, error) {
	return &domain.LoginResult{}, nil
}

type fakeRefreshExecutor struct{}

func (fakeRefreshExecutor) LoginWithRefreshToken(context.Context, string, string) (*domain.LoginResult, error) {
	return &domain.LoginResult{}, nil
}

type fakeStorage struct{}

func (fakeStorage) SaveToken(*domain.LoginResult) error { return nil }

func (fakeStorage) LoadToken(string) (*domain.LoginResult, error) {
	return nil, errors.New("no token stored")
}

func newServeTestClient() *clientgrpc.Client {
	transport := clientgrpc.NewTransport()
	session := usecase.NewSession(fakeStorage{}, fakeRefreshExecutor{})
	return clientgrpc.NewClient(transport, session)
}

func newTestAuth() *usecase.Auth {
	return usecase.NewAuth(fakeLoginExecutor{}, fakeStorage{}, nil)
}

func TestServeRepoOps_BuildsEveryRunner(t *testing.T) {
	ops := serveRepoOps(newTestAuth(), newServeTestClient())
	require.NotNil(t, ops.Update)
	require.NotNil(t, ops.Push)
	require.NotNil(t, ops.Merge)
	require.NotNil(t, ops.Revert)
	require.NotNil(t, ops.Diff)
	require.NotNil(t, ops.Proxy)
}

func TestServeCommand_Flags(t *testing.T) {
	cmd := newServeCommand(newTestAuth(), newServeTestClient, fakeStorage{}, noSigners)
	require.Equal(t, "serve", cmd.Use)
	require.NotNil(t, cmd.Flags().Lookup("port"))
	require.NotNil(t, cmd.Flags().Lookup("endpoint"))
}

func TestServeCommand_StartsAndStopsOnCancelledContext(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	cmd := newServeCommand(newTestAuth(), newServeTestClient, fakeStorage{}, noSigners)
	cmd.SetArgs([]string{"--port", "0", "--endpoint", endpoint})
	cmd.SetOut(io.Discard)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	require.NoError(t, cmd.Execute())
	_, err := os.Stat(endpoint)
	require.ErrorIs(t, err, os.ErrNotExist, "the endpoint is removed on graceful stop")
}

func TestServeCommand_InvalidPortFails(t *testing.T) {
	cmd := newServeCommand(newTestAuth(), newServeTestClient, fakeStorage{}, noSigners)
	cmd.SetArgs([]string{"--port", "70000", "--endpoint", filepath.Join(t.TempDir(), "daemon.json")})
	cmd.SetOut(io.Discard)

	require.Error(t, cmd.Execute())
}

func noSigners() ([]sshkey.Signer, error) { return nil, nil }

type storedToken struct {
	token *domain.LoginResult
}

func (s storedToken) SaveToken(*domain.LoginResult) error { return nil }
func (s storedToken) LoadToken(string) (*domain.LoginResult, error) {
	if s.token == nil {
		return nil, errors.New("no token stored")
	}
	return s.token, nil
}

func TestServeAccounts_Session(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u7"}`))
	accounts := serveAccounts(newServeTestClient, storedToken{token: &domain.LoginResult{AccessToken: "h." + payload + ".s", Email: "ann@studio.test"}}, noSigners)
	sess, ok := accounts.Session("host")
	require.True(t, ok)
	require.Equal(t, "ann@studio.test", sess.Email)
	require.Equal(t, "u7", sess.UserID)

	_, ok = serveAccounts(newServeTestClient, storedToken{}, noSigners).Session("host")
	require.False(t, ok)
	_, ok = serveAccounts(newServeTestClient, storedToken{token: &domain.LoginResult{}}, noSigners).Session("host")
	require.False(t, ok)
}

func TestServeAccounts_FailuresBeforeTheServer(t *testing.T) {
	ctx := context.Background()
	accounts := serveAccounts(newServeTestClient, fakeStorage{}, func() ([]sshkey.Signer, error) { return nil, errors.New("agent locked") })
	require.EqualError(t, accounts.LoginSSH(ctx, "127.0.0.1:1", ""), "agent locked")

	accounts = serveAccounts(newServeTestClient, fakeStorage{}, noSigners)
	require.ErrorContains(t, accounts.LoginSSH(ctx, "127.0.0.1:1", ""), "no SSH key available")
	require.Error(t, accounts.Clone(ctx, "not a url", "", nil, t.TempDir(), nil))
	conn, err := accounts.Connect(ctx, "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	target := filepath.Join(t.TempDir(), "busy")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "x"), []byte("x"), 0o644))
	require.Error(t, accounts.Clone(ctx, "http://127.0.0.1:1/org/project/art", "", []string{"audio"}, target, nil))

	login := serveLogin(newServeTestClient, fakeStorage{})
	require.Error(t, login(ctx, "ftp://host", "ann", "pw"))
	require.ErrorContains(t, login(ctx, "127.0.0.1:1", "", "pw"), "username cannot be empty")
}

func TestDaemonInput(t *testing.T) {
	_, _, err := daemonInput{}.PromptUsernameAndPassword()
	require.ErrorContains(t, err, "sign in")
}
