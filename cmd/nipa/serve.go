package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/daemon"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func newServeCommand(auth *usecase.Auth, client *clientgrpc.Client) *cobra.Command {
	var port int
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
				Login:        auth.LoginWithUsernamePassword,
				Runners:      serveRunners(auth, client),
			})
			if err != nil {
				return err
			}
			ep := srv.Endpoint()
			cmd.Printf("nipa daemon listening on 127.0.0.1:%d (pid %d)\n", ep.Port, ep.PID)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Serve(ctx)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "loopback port to bind (0 picks a free port)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "discovery file path (default ~/.config/nipa/daemon.json)")
	return cmd
}

// serveRunners builds the per-root usecase graph for the daemon, mirroring how
// the CLI wires one per process.
func serveRunners(auth *usecase.Auth, client *clientgrpc.Client) daemon.Runners {
	return daemon.Runners{
		Update: func() daemon.UpdateRunner {
			return usecase.NewUpdate(auth, client, localrepo.NewLocalRepo())
		},
		Push: func() daemon.PushRunner {
			return usecase.NewPush(auth, client, localrepo.NewLocalRepo())
		},
		Merge: func() daemon.MergeRunner {
			push := usecase.NewPush(auth, client, localrepo.NewLocalRepo())
			return usecase.NewMerge(auth, client, localrepo.NewLocalRepo(), push)
		},
		Revert: func() daemon.RevertRunner {
			push := usecase.NewPush(auth, client, localrepo.NewLocalRepo())
			return usecase.NewRevert(auth, client, localrepo.NewLocalRepo(), push)
		},
		Diff: func() daemon.DiffRunner {
			return usecase.NewDiff(auth, client, localrepo.NewLocalRepo())
		},
	}
}
