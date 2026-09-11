package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Push staged changes to the server",
		Long:          "Upload the content of files marked with nipa add and commit them on the configured branch.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			message, _ := cmd.Flags().GetString("message")
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			return c.useCase.Push().Run(cmd.Context(), root, message)
		},
	}
	cmd.Flags().StringP("message", "m", "", "Commit message for the push (required)")
	return cmd
}
