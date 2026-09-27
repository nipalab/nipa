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

func newServeCommand(auth *usecase.Auth, newClient func() *clientgrpc.Client) *cobra.Command {
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
				Runners: daemon.Runners{New: func(string) daemon.RepoOps {
					return serveRepoOps(auth, newClient())
				}},
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

// serveRepoOps builds the operation graph of one watched root over a dedicated
// client, mirroring how the CLI wires its usecases per process.
func serveRepoOps(auth *usecase.Auth, client *clientgrpc.Client) daemon.RepoOps {
	push := usecase.NewPush(auth, client, localrepo.NewLocalRepo())
	return daemon.RepoOps{
		Update: usecase.NewUpdate(auth, client, localrepo.NewLocalRepo()),
		Push:   push,
		Merge:  usecase.NewMerge(auth, client, localrepo.NewLocalRepo(), push),
		Revert: usecase.NewRevert(auth, client, localrepo.NewLocalRepo(), push),
		Diff:   usecase.NewDiff(auth, client, localrepo.NewLocalRepo()),
		Proxy:  client,
	}
}
