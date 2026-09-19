package cli

import (
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

func (c *Cli) setupRevertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "revert <commit>",
		Short:         "Revert one or more commits",
		Long:          "Create new commits that undo the changes introduced by the given commit or range of commits (newest first, up to 16 commits), without rewriting history. Conflicting files are left with merge markers; resolve them and run nipa revert --continue (or nipa push for a single commit).",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := revertOptionsFromFlags(cmd)
			modes := 0
			if opts.Abort {
				modes++
			}
			if opts.Continue {
				modes++
			}
			if opts.Skip {
				modes++
			}
			if modes > 1 {
				return domain.NewUserError("--abort, --continue and --skip are mutually exclusive")
			}
			target := ""
			switch {
			case modes == 0 && len(args) != 1:
				return domain.NewUserError("a commit to revert is required (or use --continue, --abort or --skip)")
			case modes > 0 && len(args) != 0:
				return domain.NewUserError("--continue, --abort and --skip do not take a commit argument")
			case len(args) == 1:
				target = args[0]
			}

			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			outcome, err := c.useCase.Revert().Run(cmd.Context(), root, target, opts, newProgressRenderer(cmd.OutOrStdout()))
			if err != nil {
				return err
			}
			printRevertOutcome(cmd, opts, outcome)
			if len(outcome.Conflicts) > 0 {
				cmd.Printf("Automatic revert failed; the following files conflict:\n")
				for _, p := range outcome.Conflicts {
					cmd.Printf("  C %s\n", p)
				}
				return domain.NewUserError("revert conflicts; resolve the files above and run nipa revert --continue")
			}
			return nil
		},
	}
	cmd.Flags().Bool("continue", false, "Continue a revert that stopped on conflicts")
	cmd.Flags().Bool("abort", false, "Cancel a revert in progress and restore the working copy")
	cmd.Flags().Bool("skip", false, "Skip the commit that conflicts and continue the revert")
	cmd.Flags().Bool("no-commit", false, "Apply the revert to the working copy without committing")
	cmd.Flags().Int("mainline", 0, "Mainline parent (1 or 2) when reverting a merge commit")
	cmd.Flags().StringP("message", "m", "", "Message for the revert commit (single commit only)")
	return cmd
}

func revertOptionsFromFlags(cmd *cobra.Command) usecase.RevertOptions {
	abort, _ := cmd.Flags().GetBool("abort")
	cont, _ := cmd.Flags().GetBool("continue")
	skip, _ := cmd.Flags().GetBool("skip")
	noCommit, _ := cmd.Flags().GetBool("no-commit")
	mainline, _ := cmd.Flags().GetInt("mainline")
	message, _ := cmd.Flags().GetString("message")
	return usecase.RevertOptions{
		Abort:    abort,
		Continue: cont,
		Skip:     skip,
		NoCommit: noCommit,
		Mainline: mainline,
		Message:  message,
	}
}

func printRevertOutcome(cmd *cobra.Command, opts usecase.RevertOptions, outcome *usecase.RevertOutcome) {
	if opts.Abort {
		cmd.Printf("Revert aborted; the working copy was restored.\n")
		return
	}
	if len(outcome.Conflicts) > 0 {
		return
	}
	if opts.Skip {
		cmd.Printf("Revert step skipped.\n")
		return
	}
	switch {
	case outcome.NoChange:
		cmd.Printf("Nothing to revert; the changes are already reverted.\n")
	case opts.NoCommit:
		cmd.Printf("Revert applied; changes are staged, run nipa push to commit them.\n")
	case outcome.Committed:
		cmd.Printf("Revert committed.\n")
	}
}
