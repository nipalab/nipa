package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update",
		Short:         "Fetch and apply the latest changes from the server",
		Long:          "Download the current tree of the configured branch, materialize changed files in the working copy, and refresh the local snapshot. Refuses to run, before changing anything, when a file it would replace has local changes; --force discards them.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			return c.useCase.Update().WithForce(force).Run(cmd.Context(), root, newProgressRenderer(cmd.OutOrStdout()))
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite locally changed files the update replaces instead of refusing")
	return cmd
}
