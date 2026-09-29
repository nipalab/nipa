package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Push staged changes to the server",
		Long:          "Upload the content of files marked with nipa add and commit them on the configured branch. With --dry-run, report the file changes and the upload estimate without contacting the server, uploading anything or changing local state.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			message, _ := cmd.Flags().GetString("message")
			dryRun, err := dryRunRequested(cmd)
			if err != nil {
				return err
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			if dryRun {
				plan, err := c.useCase.Push().Plan(cmd.Context(), root)
				if err != nil {
					return err
				}
				return writePlan(cmd, plan)
			}
			return c.useCase.Push().Run(cmd.Context(), root, message, newProgressRenderer(cmd.OutOrStdout()))
		},
	}
	cmd.Flags().StringP("message", "m", "", "Commit message for the push (required)")
	addDryRunFlags(cmd)
	return cmd
}
