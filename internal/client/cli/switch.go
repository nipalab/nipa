package cli

import (
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupSwitchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "switch [branch]",
		Short:         "Switch the working copy to another branch or a tag",
		Long:          "Fetch the tree of the given branch or tag, materialize it in the working copy, and point the local repository at it. Switching to a tag detaches HEAD at that tag. Refuses to run while changes are staged.",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tagName, _ := cmd.Flags().GetString("tag")
			cfg, err := c.loadConfig()
			if err != nil {
				return err
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			if tagName != "" {
				if len(args) != 0 {
					return domain.NewUserError("--tag cannot be combined with a branch name")
				}
				if err := c.useCase.Update().SwitchTag(cmd.Context(), root, tagName, newProgressRenderer(cmd.OutOrStdout())); err != nil {
					return err
				}
				cmd.Printf("HEAD detached at tag %q\n", tagName)
				return nil
			}
			if len(args) != 1 {
				return domain.NewUserError("a branch name is required (or use --tag)")
			}
			branch := args[0]
			if cfg.Branch == branch && cfg.Head == nil {
				cmd.Printf("Already on branch %q\n", branch)
				return nil
			}
			if err := c.useCase.Update().Switch(cmd.Context(), root, branch, newProgressRenderer(cmd.OutOrStdout())); err != nil {
				return err
			}
			cmd.Printf("Switched to branch %q\n", branch)
			return nil
		},
	}
	cmd.Flags().String("tag", "", "Switch to a tag instead of a branch (detaches HEAD)")
	return cmd
}
