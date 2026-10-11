package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/version"
	"github.com/spf13/cobra"
)

type versionPair struct {
	Client version.Info `json:"client"`
	Server version.Info `json:"server"`
}

func (c *Cli) setupVersionCmd() *cobra.Command {
	var server bool
	cmd := &cobra.Command{
		Use:           "version [url]",
		Short:         "Show the nipa version",
		Long:          "Show the version of the local nipa client, or with --server the version of the server at the given URL (or the remote of the current clone).",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			local := version.Resolve()
			if !server {
				if jsonRequested(cmd) {
					return output.WriteJSON(cmd.OutOrStdout(), local)
				}
				cmd.Printf("nipa version %s\n", local)
				return nil
			}
			url, err := versionServerURL(args)
			if err != nil {
				return err
			}
			remote, err := c.useCase.Version().Server(cmd.Context(), url)
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), versionPair{Client: local, Server: *remote})
			}
			cmd.Printf("client: %s\n", local)
			cmd.Printf("server: %s\n", *remote)
			if local.Version != remote.Version {
				cmd.PrintErrf("note: client and server versions differ\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&server, "server", false, "Query the version of the nipa server instead")
	addJSONFlag(cmd)
	return cmd
}

func versionServerURL(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return "", err
	}
	cfg, err := localrepo.NewLocalRepoWithTarget(root).LoadConfig()
	if err != nil {
		return "", err
	}
	return cfg.Url, nil
}
