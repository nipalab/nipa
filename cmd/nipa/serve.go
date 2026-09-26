package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func newServeCommand(auth *usecase.Auth) *cobra.Command {
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
