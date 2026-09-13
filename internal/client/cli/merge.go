package cli

import (
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

func (c *Cli) setupMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "merge <branch>",
		Short:         "Merge a branch into the current branch",
		Long:          "Merge another branch into the current branch. Fast-forwards when the current branch has not moved, otherwise creates a merge commit from a 3-way merge of the two branch trees. Conflicting files are left with merge markers for you to resolve and push.",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := mergeOptionsFromFlags(cmd)
			if !opts.Abort && len(args) != 1 {
				return domain.NewUserError("a source branch to merge is required (or use --abort)")
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			source := ""
			if len(args) == 1 {
				source = args[0]
			}
			outcome, err := c.useCase.Merge().Run(cmd.Context(), root, source, opts)
			if err != nil {
				return err
			}
			if opts.Abort {
				cmd.Printf("Merge aborted; the working copy was restored.\n")
				return nil
			}
			switch {
			case outcome.UpToDate:
				cmd.Printf("Already up to date.\n")
			case outcome.FastForwarded:
				cmd.Printf("Fast-forwarded current branch to %q.\n", source)
			case outcome.MergeCommitted:
				cmd.Printf("Merge committed.\n")
			}
			if len(outcome.Conflicts) > 0 {
				cmd.Printf("Automatic merge failed; the following files conflict:\n")
				for _, p := range outcome.Conflicts {
					cmd.Printf("  C %s\n", p)
				}
				return domain.NewUserError("merge conflicts; resolve the files above and run nipa push")
			}
			return nil
		},
	}
	cmd.Flags().Bool("abort", false, "Cancel a merge that stopped on conflicts")
	cmd.Flags().Bool("ff-only", false, "Error instead of creating a merge commit")
	cmd.Flags().Bool("no-ff", false, "Create a merge commit even when a fast-forward is possible")
	cmd.Flags().StringP("message", "m", "", "Message for the merge commit")
	return cmd
}

func mergeOptionsFromFlags(cmd *cobra.Command) usecase.MergeOptions {
	abort, _ := cmd.Flags().GetBool("abort")
	ffOnly, _ := cmd.Flags().GetBool("ff-only")
	noFF, _ := cmd.Flags().GetBool("no-ff")
	message, _ := cmd.Flags().GetString("message")
	return usecase.MergeOptions{
		Abort:   abort,
		FFOnly:  ffOnly,
		NoFF:    noFF,
		Message: message,
	}
}
