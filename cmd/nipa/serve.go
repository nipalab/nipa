package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/sshkey"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func newServeCommand(auth *usecase.Auth, newClient func() *clientgrpc.Client, storage tokenStorage, signers func() ([]sshkey.Signer, error)) *cobra.Command {
	var port, jsonPort int
	var endpoint string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local daemon that GUI clients connect to",
		Long: "Serve the loopback gRPC API consumed by the desktop client, Explorer\n" +
			"integration and game-engine plugins. The daemon publishes its port and\n" +
			"capability token in ~/.config/nipa/daemon.json and stops on SIGINT/SIGTERM.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv, err := daemon.NewServer(daemon.Options{
				EndpointPath: endpoint,
				Port:         port,
				JSONPort:     jsonPort,
				Login:        serveLogin(newClient, storage),
				Runners: daemon.Runners{New: func(string) daemon.RepoOps {
					return serveRepoOps(auth, newClient())
				}},
				Accounts: serveAccounts(newClient, storage, signers),
			})
			if err != nil {
				return err
			}
			ep := srv.Endpoint()
			cmd.Printf("nipa daemon listening on 127.0.0.1:%d, JSON bridge on 127.0.0.1:%d (pid %d)\n", ep.Port, ep.JSONPort, ep.PID)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Serve(ctx)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "loopback port to bind (0 picks a free port)")
	cmd.Flags().IntVar(&jsonPort, "json-port", 0, "loopback port of the JSON/HTTP bridge (0 picks a free port; env NIPA_DAEMON_JSON_PORT)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "discovery file path (default ~/.config/nipa/daemon.json)")
	return cmd
}

// serveRepoOps builds the operation graph of one watched root over a dedicated
// client, mirroring how the CLI wires its usecases per process.
func serveRepoOps(auth *usecase.Auth, client *clientgrpc.Client) daemon.RepoOps {
	push := usecase.NewPush(auth, client, localrepo.NewLocalRepo())
	return daemon.RepoOps{
		Update:     usecase.NewUpdate(auth, client, localrepo.NewLocalRepo()),
		Push:       push,
		Merge:      usecase.NewMerge(auth, client, localrepo.NewLocalRepo(), push),
		Revert:     usecase.NewRevert(auth, client, localrepo.NewLocalRepo(), push),
		Diff:       daemon.SerialDiff(usecase.NewDiff(auth, client, localrepo.NewLocalRepo())),
		Proxy:      client,
		Pins:       usecase.NewLockPins(auth, client, localrepo.NewLocalRepo()),
		Auth:       auth,
		Downloader: client,
	}
}

// daemonInput replaces the terminal prompter in the daemon: it has no
// terminal, so a missing session must be fixed by signing in from the app.
type daemonInput struct{}

func (daemonInput) PromptUsernameAndPassword() (string, string, error) {
	return "", "", domain.NewTokenError("sign in to this server first")
}

func connectClient(ctx context.Context, newClient func() *clientgrpc.Client, address string) (*clientgrpc.Client, error) {
	c := newClient()
	if err := c.Connect(ctx, address); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// serveLogin signs in over a fresh connection to the server being logged into.
func serveLogin(newClient func() *clientgrpc.Client, storage tokenStorage) daemon.LoginFunc {
	return func(ctx context.Context, address, username, password string) error {
		ep, err := domain.ParseEndpoint(address)
		if err != nil {
			return err
		}
		c, err := connectClient(ctx, newClient, ep.Address)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		return usecase.NewAuth(c, storage, daemonInput{}).LoginWithUsernamePassword(ctx, ep.Address, username, password)
	}
}

func serveAccounts(newClient func() *clientgrpc.Client, storage tokenStorage, signers func() ([]sshkey.Signer, error)) daemon.Accounts {
	auth := func(c *clientgrpc.Client) *usecase.Auth {
		return usecase.NewAuth(c, storage, daemonInput{}).WithSSHLogin(signers)
	}
	return daemon.Accounts{
		Session: func(host string) (daemon.Session, bool) {
			token, err := storage.LoadToken(host)
			if err != nil || token == nil || token.AccessToken == "" {
				return daemon.Session{}, false
			}
			id, _ := clientgrpc.TokenSubject(token.AccessToken)
			return daemon.Session{Email: token.Email, UserID: id}, true
		},
		LoginSSH: func(ctx context.Context, address string, login daemon.SSHLogin) error {
			keys, err := loginSigners(signers, login)
			if err != nil {
				return err
			}
			c, err := connectClient(ctx, newClient, address)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			return auth(c).LoginWithSSHKey(ctx, address, login.Email, keys)
		},
		Connect: func(ctx context.Context, address string) (daemon.RemoteConn, error) {
			return connectClient(ctx, newClient, address)
		},
		Clone: func(ctx context.Context, rawURL, branch string, sparse []string, target string, progress usecase.DownloadProgress) error {
			nu, err := domain.ParseNipaUrl(rawURL)
			if err != nil {
				return err
			}
			c, err := connectClient(ctx, newClient, nu.Origin)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			paths := append([]string{}, sparse...)
			if nu.Path != "" {
				paths = append(paths, nu.Path)
			}
			lr := localrepo.NewLocalRepo()
			defer func() { _ = lr.Close() }()
			return usecase.NewRepo(auth(c), c, lr).Clone(ctx, nu.Url, nu.Host, nu.Org, nu.Project, branch, paths, target, progress)
		},
	}
}

func loginSigners(signers func() ([]sshkey.Signer, error), login daemon.SSHLogin) ([]sshkey.Signer, error) {
	if login.KeyPath == "" {
		return signers()
	}
	signer, err := sshkey.Load(login.KeyPath, login.Passphrase)
	if err != nil {
		return nil, err
	}
	return []sshkey.Signer{signer}, nil
}
