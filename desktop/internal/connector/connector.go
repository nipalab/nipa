// Package connector attaches the desktop app to the local `nipa serve`
// daemon, starting one when no live daemon is published.
package connector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nipalab/nipa/desktop/internal/update"
	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const (
	EnvNipaBinary       = "NIPA_BIN"
	defaultStartTimeout = 10 * time.Second
	pingTimeout         = 2 * time.Second
	pollInterval        = 100 * time.Millisecond
)

var (
	ErrBinaryNotFound = errors.New("nipa CLI not found: install it next to the app, on PATH, or set NIPA_BIN")
	errStaleEndpoint  = errors.New("daemon endpoint is stale")
	errOutdated       = errors.New("only an outdated daemon answers")
)

type Options struct {
	// EndpointPath overrides ~/.config/nipa/daemon.json.
	EndpointPath string
	// Binary is the nipa CLI used to start a daemon; empty resolves NIPA_BIN,
	// a binary next to the app, then PATH.
	Binary string
	// LogPath receives the started daemon's output; empty discards it.
	LogPath      string
	StartTimeout time.Duration
	// MinVersion retires a daemon reporting an older release (the app was
	// updated while the previous daemon kept running) and starts a new one.
	MinVersion string

	start      func(bin, logPath, endpoint string) error
	lookPath   func(string) (string, error)
	executable func() (string, error)
	alive      func(pid int) bool
}

type Conn struct {
	Client   daemonpb.NipaDaemonClient
	Endpoint daemon.Endpoint
	Version  string
	Started  bool
	cc       *grpc.ClientConn
}

func (c *Conn) Close() error {
	return c.cc.Close()
}

// Connect attaches to the published daemon or starts `nipa serve` and waits
// until it answers Ping.
func Connect(ctx context.Context, opts Options) (*Conn, error) {
	opts = withDefaults(opts)
	path := opts.EndpointPath
	if path == "" {
		var err error
		if path, err = daemon.EndpointPath(); err != nil {
			return nil, err
		}
	}
	if conn, err := attach(ctx, path, opts.alive); err == nil {
		if !update.Older(conn.Version, opts.MinVersion) {
			return conn, nil
		}
		if _, err := conn.Client.Shutdown(ctx, &daemonpb.ShutdownRequest{}); err != nil {
			return conn, nil
		}
		waitStopped(ctx, conn, opts.StartTimeout)
		_ = conn.Close()
	}

	bin, err := resolveBinary(opts)
	if err != nil {
		return nil, err
	}
	if err := opts.start(bin, opts.LogPath, opts.EndpointPath); err != nil {
		return nil, fmt.Errorf("start %s serve: %w", bin, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, opts.StartTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastErr error
	outdated := false
	for {
		conn, err := attach(waitCtx, path, opts.alive)
		if err == nil && !update.Older(conn.Version, opts.MinVersion) {
			conn.Started = true
			return conn, nil
		}
		if err == nil {
			// The retired daemon is still draining and published.
			_ = conn.Close()
			outdated = true
			err = errOutdated
		}
		lastErr = err
		select {
		case <-waitCtx.Done():
			if outdated {
				if conn, err := attach(ctx, path, opts.alive); err == nil {
					return conn, nil
				}
			}
			return nil, fmt.Errorf("nipa daemon did not start: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func withDefaults(opts Options) Options {
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = defaultStartTimeout
	}
	if opts.start == nil {
		opts.start = startDetached
	}
	if opts.lookPath == nil {
		opts.lookPath = exec.LookPath
	}
	if opts.executable == nil {
		opts.executable = os.Executable
	}
	if opts.alive == nil {
		opts.alive = daemon.ProcessAlive
	}
	return opts
}

func attach(ctx context.Context, path string, alive func(int) bool) (*Conn, error) {
	ep, err := daemon.ReadEndpoint(path)
	if err != nil {
		return nil, err
	}
	if ep.Port <= 0 || !alive(ep.PID) {
		return nil, errStaleEndpoint
	}
	cc, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", ep.Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(tokenCredentials(ep.Token)))
	if err != nil {
		return nil, err
	}
	client := daemonpb.NewNipaDaemonClient(cc)
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	pong, err := client.Ping(pingCtx, &daemonpb.PingRequest{})
	if err != nil {
		_ = cc.Close()
		return nil, err
	}
	return &Conn{Client: client, Endpoint: *ep, Version: pong.GetVersion(), cc: cc}, nil
}

// waitStopped waits until a daemon asked to shut down stops answering; a
// slow drain is not an error, the new daemon replaces the endpoint anyway.
func waitStopped(ctx context.Context, conn *Conn, timeout time.Duration) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		pingCtx, stop := context.WithTimeout(waitCtx, pingTimeout)
		_, err := conn.Client.Ping(pingCtx, &daemonpb.PingRequest{})
		stop()
		if err != nil {
			return
		}
		select {
		case <-waitCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

func resolveBinary(opts Options) (string, error) {
	if opts.Binary != "" {
		return opts.Binary, nil
	}
	if bin := os.Getenv(EnvNipaBinary); bin != "" {
		return bin, nil
	}
	name := "nipa"
	if runtime.GOOS == "windows" {
		name = "nipa.exe"
	}
	if exe, err := opts.executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), name)
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	if bin, err := opts.lookPath(name); err == nil {
		return bin, nil
	}
	return "", ErrBinaryNotFound
}

func startDetached(bin, logPath, endpoint string) error {
	args := []string{"serve"}
	if endpoint != "" {
		args = append(args, "--endpoint", endpoint)
	}
	cmd := exec.Command(bin, args...)
	detach(cmd)
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
			return err
		}
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = log.Close() }()
		cmd.Stdout, cmd.Stderr = log, log
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

type tokenCredentials string

func (t tokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{daemon.TokenHeader: string(t)}, nil
}

func (tokenCredentials) RequireTransportSecurity() bool { return false }
