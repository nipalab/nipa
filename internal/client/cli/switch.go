package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupSwitchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "switch [branch]",
		Short:         "Switch the working copy to another branch",
		Long:          "Fetch the tree of the given branch, materialize it in the working copy, and point the local repository at it. Refuses to run while changes are staged.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := args[0]
			cfg, err := c.loadConfig()
			if err != nil {
				return err
			}
			if cfg.Branch == branch {
				cmd.Printf("Already on branch %q\n", branch)
				return nil
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			if err := c.useCase.Update().Switch(cmd.Context(), root, branch, newProgressRenderer(cmd.OutOrStdout())); err != nil {
				return err
			}
			cmd.Printf("Switched to branch %q\n", branch)
			return nil
		},
	}
	return cmd
}
