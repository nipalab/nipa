package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/sshkey"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/version"
)

var frozenStatus = &domain.MaintenanceStatus{Server: &domain.MaintenanceWindow{Mode: "frozen", Active: true, Message: "moving the database"}}

func frozenRefusal() error {
	return domain.NewMaintenanceError(maintenance.Info{Scope: maintenance.ScopeServer, Mode: maintenance.Frozen}, nil)
}

type stubMaintenanceProbe struct {
	status *domain.MaintenanceStatus
	err    error
	calls  int
}

func (s *stubMaintenanceProbe) GetMaintenanceStatus(context.Context, string, string) (*domain.MaintenanceStatus, error) {
	s.calls++
	return s.status, s.err
}

type probingRefreshExecutor struct {
	*stubRefreshExecutor
	*stubMaintenanceProbe
}

type probingLoginExecutor struct {
	*stubFullExecutor
	*stubMaintenanceProbe
}

func TestSignInPaused(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, signInPaused(ctx, &stubRefreshExecutor{}), "no probe, no answer")
	require.NoError(t, signInPaused(ctx, &stubMaintenanceProbe{err: errors.New("down")}))
	require.NoError(t, signInPaused(ctx, &stubMaintenanceProbe{status: &domain.MaintenanceStatus{}}))
	require.NoError(t, signInPaused(ctx, &stubMaintenanceProbe{status: &domain.MaintenanceStatus{Server: &domain.MaintenanceWindow{Mode: "full", Active: true}}}), "only frozen refuses sign-in")
	require.NoError(t, signInPaused(ctx, &stubMaintenanceProbe{status: &domain.MaintenanceStatus{Server: &domain.MaintenanceWindow{Mode: "frozen"}}}), "a scheduled window does not")
	err := signInPaused(ctx, &stubMaintenanceProbe{status: frozenStatus})
	require.True(t, domain.IsMaintenance(err))
	require.Equal(t, "server is in maintenance (frozen): moving the database", err.Error())
}

func TestSession_Reauth_FrozenDoesNotPrompt(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "tok", Host: "example.com"}}
	probe := &stubMaintenanceProbe{status: frozenStatus}
	executor := &probingRefreshExecutor{stubRefreshExecutor: &stubRefreshExecutor{}, stubMaintenanceProbe: probe}
	input := &stubUserInput{username: "alice", password: "secret"}
	session := NewSession(storage, executor, input)

	_, err := session.Refresh(context.Background(), "example.com")
	require.True(t, domain.IsMaintenance(err), "got %v", err)
	require.Equal(t, 1, probe.calls)
	require.Empty(t, executor.lastUsername, "no password is asked for while sign-in is paused")
	require.Empty(t, storage.savedTokens)
}

func TestSession_Reauth_NotFrozenPrompts(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "tok", Host: "example.com"}}
	executor := &probingRefreshExecutor{
		stubRefreshExecutor:  &stubRefreshExecutor{passwordResult: &domain.LoginResult{AccessToken: "new"}},
		stubMaintenanceProbe: &stubMaintenanceProbe{status: &domain.MaintenanceStatus{}},
	}
	session := NewSession(storage, executor, &stubUserInput{username: "alice", password: "secret"})

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "new", token)
}

func TestSession_Reauth_MaintenanceCause(t *testing.T) {
	session := NewSession(&stubSecureStorage{}, &stubRefreshExecutor{}, &stubUserInput{username: "alice"})
	_, err := session.reauth(context.Background(), "example.com", frozenRefusal())
	require.True(t, domain.IsMaintenance(err))
}

func TestSession_ReauthViaSSH_Maintenance(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "tok", Host: "example.com"}}
	executor := &stubSSHSessionExecutor{
		refresh: &stubRefreshExecutor{},
		ssh:     &stubSSHExecutor{beginErr: frozenRefusal()},
	}
	session := NewSession(storage, executor, &stubUserInput{username: "alice", password: "secret"}).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	_, err := session.Refresh(context.Background(), "example.com")
	require.True(t, domain.IsMaintenance(err))
	require.Empty(t, executor.refresh.lastUsername, "the SSH refusal stops before the password prompt")
}

func TestAuth_MakeSureLoggedIn_FrozenDoesNotPrompt(t *testing.T) {
	probe := &stubMaintenanceProbe{status: frozenStatus}
	login := &stubLoginExecutor{}
	executor := &probingLoginExecutor{stubFullExecutor: &stubFullExecutor{stubLoginExecutor: login, stubSSHExecutor: &stubSSHExecutor{}}, stubMaintenanceProbe: probe}
	input := &stubUserInput{username: "alice", password: "secret"}
	auth := NewAuth(executor, &stubSecureStorage{loadErr: errors.New("none")}, input)

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.True(t, domain.IsMaintenance(err))
	require.Empty(t, login.lastUsername)
}

func TestAuth_MakeSureLoggedIn_SSHMaintenanceStops(t *testing.T) {
	login := &stubLoginExecutor{}
	executor := &stubFullExecutor{stubLoginExecutor: login, stubSSHExecutor: &stubSSHExecutor{beginErr: frozenRefusal()}}
	auth := NewAuth(executor, &stubSecureStorage{loadErr: errors.New("none")}, &stubUserInput{username: "alice", password: "secret"}).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.True(t, domain.IsMaintenance(err))
	require.Empty(t, login.lastUsername)
}

type stubVersionClient struct {
	stubMaintenanceProbe
	connected  string
	connectErr error
}

func (s *stubVersionClient) Connect(_ context.Context, host string) error {
	s.connected = host
	return s.connectErr
}

func (s *stubVersionClient) GetServerVersion(context.Context) (*version.Info, error) {
	return &version.Info{}, nil
}

func TestVersion_Maintenance(t *testing.T) {
	client := &stubVersionClient{stubMaintenanceProbe: stubMaintenanceProbe{status: frozenStatus}}
	v := NewVersion(client)

	st, err := v.Maintenance(context.Background(), "https://nipa.example.com/acme/game")
	require.NoError(t, err)
	require.Same(t, frozenStatus, st)
	require.Equal(t, "https://nipa.example.com", client.connected)

	_, err = v.Maintenance(context.Background(), "::bad")
	require.Error(t, err)

	client.connectErr = errors.New("dial")
	_, err = v.Maintenance(context.Background(), "https://nipa.example.com/acme/game")
	require.EqualError(t, err, "dial")
}
