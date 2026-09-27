package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
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
	cmd := newServeCommand(newTestAuth(), newServeTestClient)
	require.Equal(t, "serve", cmd.Use)
	require.NotNil(t, cmd.Flags().Lookup("port"))
	require.NotNil(t, cmd.Flags().Lookup("endpoint"))
}

func TestServeCommand_StartsAndStopsOnCancelledContext(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	cmd := newServeCommand(newTestAuth(), newServeTestClient)
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
	cmd := newServeCommand(newTestAuth(), newServeTestClient)
	cmd.SetArgs([]string{"--port", "70000", "--endpoint", filepath.Join(t.TempDir(), "daemon.json")})
	cmd.SetOut(io.Discard)

	require.Error(t, cmd.Execute())
}
