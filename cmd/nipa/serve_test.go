package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/aicontext"
	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/sshkey"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
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
	require.NotNil(t, ops.Generate)
	require.NotNil(t, ops.Restore)
}

func TestServeGenerator_BuildsCollector(t *testing.T) {
	gen := serveGenerator(newTestAuth(), newServeTestClient())
	src, err := gen.Sources("/work/game")
	require.NoError(t, err)
	col, ok := src.(*aicontext.Collector)
	require.True(t, ok)
	require.NotNil(t, col.Diff)
	require.NotNil(t, col.History)
	require.NotNil(t, col.Connector)
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
	require.EqualError(t, accounts.LoginSSH(ctx, "127.0.0.1:1", daemon.SSHLogin{}), "agent locked")

	accounts = serveAccounts(newServeTestClient, fakeStorage{}, noSigners)
	require.ErrorContains(t, accounts.LoginSSH(ctx, "127.0.0.1:1", daemon.SSHLogin{}), "no SSH key available")
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

type sshLoginServer struct {
	pb.UnimplementedNipaServiceServer
	registered ssh.PublicKey
	audience   string
	seen       [][]byte
}

func (s *sshLoginServer) BeginLoginSSHKey(context.Context, *pb.BeginLoginSSHKeyRequest) (*pb.BeginLoginSSHKeyResponse, error) {
	return &pb.BeginLoginSSHKeyResponse{Nonce: "n1", Audience: s.audience, ExpiresIn: 60}, nil
}

func (s *sshLoginServer) CompleteLoginSSHKey(_ context.Context, req *pb.CompleteLoginSSHKeyRequest) (*pb.LoginResponse, error) {
	s.seen = append(s.seen, req.GetPublicKey())
	if !bytes.Equal(req.GetPublicKey(), s.registered.Marshal()) {
		return nil, status.Error(codes.InvalidArgument, "invalid email or SSH key")
	}
	return &pb.LoginResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 60}, nil
}

func startSSHLoginServer(t *testing.T, registered ssh.PublicKey) (*sshLoginServer, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fake := &sshLoginServer{registered: registered, audience: lis.Addr().String()}
	srv := grpc.NewServer()
	pb.RegisterNipaServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return fake, lis.Addr().String()
}

func writeTestKey(t *testing.T, dir, name, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	pubKey, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	require.NoError(t, err)
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, pem.EncodeToMemory(block), 0o600))
	pub, err := ssh.NewPublicKey(pubKey)
	require.NoError(t, err)
	return p, pub
}

func TestServeAccounts_LoginSSHExplicitKey(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	const secret = "open-sesame-42"
	keyPath, pub := writeTestKey(t, dir, "id_ed25519", secret)
	strayPath, _ := writeTestKey(t, dir, "id_stray", secret)
	fake, address := startSSHLoginServer(t, pub)
	fallback := func() ([]sshkey.Signer, error) {
		t.Fatal("key_path must not fall back to other keys")
		return nil, nil
	}
	accounts := serveAccounts(newServeTestClient, fakeStorage{}, fallback)
	ctx := context.Background()

	require.NoError(t, accounts.LoginSSH(ctx, address, daemon.SSHLogin{KeyPath: keyPath, Passphrase: secret}))
	require.Len(t, fake.seen, 1)

	err := accounts.LoginSSH(ctx, address, daemon.SSHLogin{KeyPath: keyPath, Passphrase: "wrong-" + secret})
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)

	err = accounts.LoginSSH(ctx, address, daemon.SSHLogin{KeyPath: keyPath})
	require.ErrorContains(t, err, "passphrase")

	err = accounts.LoginSSH(ctx, address, daemon.SSHLogin{KeyPath: filepath.Join(dir, "missing"), Passphrase: secret})
	require.ErrorIs(t, err, os.ErrNotExist)

	err = accounts.LoginSSH(ctx, address, daemon.SSHLogin{KeyPath: strayPath, Passphrase: secret})
	require.ErrorContains(t, err, "invalid email or SSH key")
	require.Len(t, fake.seen, 2, "only the explicit key is offered")

	require.NotContains(t, logs.String(), secret)
}

func TestLoginSigners_DefaultOrder(t *testing.T) {
	called := false
	_, err := loginSigners(func() ([]sshkey.Signer, error) { called = true; return nil, nil }, daemon.SSHLogin{Email: "ann@studio.test"})
	require.NoError(t, err)
	require.True(t, called)
}
