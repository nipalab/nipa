package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update",
		Short:         "Fetch and apply the latest changes from the server",
		Long:          "Download the current tree of the configured branch, materialize changed files in the working copy, and refresh the local snapshot.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			return c.useCase.Update().Run(cmd.Context(), root, newDownloadProgress(cmd.OutOrStdout()))
		},
	}
	return cmd
}
