package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update",
		Short:         "Fetch and apply the latest changes from the server",
		Long:          "Download the current tree of the configured branch, materialize changed files in the working copy, and refresh the local snapshot. Refuses to run, before changing anything, when a file it would replace has local changes; --force discards them. When the working copy already holds the branch head, nothing is fetched or checked: use --force to restore tracked files deleted from disk.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			result, err := c.useCase.Update().WithForce(force).RunResult(cmd.Context(), root, newProgressRenderer(cmd.OutOrStdout()))
			if err != nil {
				return err
			}
			if result.UpToDate {
				cmd.Println("Already up to date.")
			}
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite locally changed files the update replaces instead of refusing, and re-check the working copy even when already up to date")
	return cmd
}
