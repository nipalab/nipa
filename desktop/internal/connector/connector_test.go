package connector

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

func serveDaemon(t *testing.T, endpoint string) *daemon.Server {
	t.Helper()
	srv, err := daemon.NewServer(daemon.Options{EndpointPath: endpoint, Version: "test-1"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	<-srv.Ready()
	return srv
}

func noStart(t *testing.T) func(string, string, string) error {
	return func(string, string, string) error {
		t.Fatal("daemon must not be started")
		return nil
	}
}

func TestConnect_AttachesToRunningDaemon(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	serveDaemon(t, endpoint)

	conn, err := Connect(context.Background(), Options{EndpointPath: endpoint, start: noStart(t)})
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	require.Equal(t, "test-1", conn.Version)
	require.False(t, conn.Started)
	require.Equal(t, os.Getpid(), conn.Endpoint.PID)

	_, err = conn.Client.ListRepos(context.Background(), &daemonpb.ListReposRequest{})
	require.NoError(t, err, "the token must authenticate every call")
}

func TestConnect_StartsDaemonWhenNoneIsPublished(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	var gotBin, gotLog, gotEndpoint string
	start := func(bin, logPath, endpoint string) error {
		gotBin, gotLog, gotEndpoint = bin, logPath, endpoint
		go func() {
			time.Sleep(150 * time.Millisecond)
			serveDaemon(t, endpoint)
		}()
		return nil
	}

	conn, err := Connect(context.Background(), Options{EndpointPath: endpoint, Binary: "/opt/nipa", LogPath: "/tmp/d.log", start: start})
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.True(t, conn.Started)
	require.Equal(t, "/opt/nipa", gotBin)
	require.Equal(t, "/tmp/d.log", gotLog)
	require.Equal(t, endpoint, gotEndpoint)
}

func TestConnect_RestartsWhenEndpointIsStale(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, daemon.WriteEndpoint(endpoint, daemon.Endpoint{PID: 1, Port: 1, Token: "old"}))
	started := false
	_, err := Connect(context.Background(), Options{
		EndpointPath: endpoint,
		Binary:       "nipa",
		StartTimeout: 200 * time.Millisecond,
		alive:        func(int) bool { return false },
		start:        func(string, string, string) error { started = true; return nil },
	})
	require.True(t, started)
	require.ErrorContains(t, err, "did not start")
}

func TestConnect_UnreachableDaemonTriggersStart(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	require.NoError(t, daemon.WriteEndpoint(endpoint, daemon.Endpoint{PID: os.Getpid(), Port: port, Token: "t"}))

	_, err = Connect(context.Background(), Options{
		EndpointPath: endpoint,
		Binary:       "nipa",
		StartTimeout: 150 * time.Millisecond,
		start:        func(string, string, string) error { return errors.New("exec failed") },
	})
	require.ErrorContains(t, err, "exec failed")
}

func TestConnect_BinaryNotFound(t *testing.T) {
	t.Setenv(EnvNipaBinary, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	_, err := Connect(context.Background(), Options{
		executable: func() (string, error) { return filepath.Join(t.TempDir(), "app"), nil },
		lookPath:   func(string) (string, error) { return "", errors.New("not on PATH") },
		start:      noStart(t),
	})
	require.ErrorIs(t, err, ErrBinaryNotFound)
}

func TestConnect_CancelledWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Connect(ctx, Options{
		EndpointPath: filepath.Join(t.TempDir(), "daemon.json"),
		Binary:       "nipa",
		start:        func(string, string, string) error { cancel(); return nil },
	})
	require.ErrorContains(t, err, "did not start")
}

func TestResolveBinaryOrder(t *testing.T) {
	dir := t.TempDir()
	name := "nipa"
	if runtime.GOOS == "windows" {
		name = "nipa.exe"
	}
	sibling := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(sibling, []byte("x"), 0o755))
	opts := withDefaults(Options{
		executable: func() (string, error) { return filepath.Join(dir, "Nipa"), nil },
		lookPath:   func(string) (string, error) { return "/usr/bin/nipa", nil },
	})

	t.Setenv(EnvNipaBinary, "/env/nipa")
	bin, err := resolveBinary(Options{Binary: "/explicit"})
	require.NoError(t, err)
	require.Equal(t, "/explicit", bin)

	bin, err = resolveBinary(opts)
	require.NoError(t, err)
	require.Equal(t, "/env/nipa", bin)

	t.Setenv(EnvNipaBinary, "")
	bin, err = resolveBinary(opts)
	require.NoError(t, err)
	require.Equal(t, sibling, bin)

	opts.executable = func() (string, error) { return "", errors.New("no exe") }
	bin, err = resolveBinary(opts)
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/nipa", bin)
}

func TestStartDetachedRunsServe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-nipa")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho \"args:$*\"\n"), 0o755))
	logPath := filepath.Join(dir, "logs", "daemon.log")

	require.NoError(t, startDetached(bin, logPath, ""))
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(data), "args:serve")
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, startDetached(bin, "", ""))
	customLog := filepath.Join(dir, "custom.log")
	require.NoError(t, startDetached(bin, customLog, "/x/daemon.json"))
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(customLog)
		return err == nil && strings.Contains(string(data), "args:serve --endpoint /x/daemon.json")
	}, 5*time.Second, 20*time.Millisecond)
	require.Error(t, startDetached(filepath.Join(dir, "missing"), "", ""))

	blocker := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	require.Error(t, startDetached(bin, filepath.Join(blocker, "sub", "d.log"), ""))
	require.Error(t, startDetached(bin, filepath.Join(dir, "logs"), ""))
}
