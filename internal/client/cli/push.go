package cli

import (
	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/localrepo"
)

func (c *Cli) setupPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Push staged changes to the server",
		Long:          "Upload the content of files marked with nipa add and commit them on the configured branch. With --dry-run, report the file changes, the upload estimate and any .nipaassets.yml budget violations of the staged files without contacting the server, uploading anything or changing local state. With --generate, the commit message is drafted by the configured OpenAI-compatible LLM from the staged text diff and asset summaries (never binary contents), shown for confirmation (--yes skips it) and tagged with a Nipa-Generated trailer; --dry-run --generate only prints the draft.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			message, _ := cmd.Flags().GetString("message")
			generate, _ := cmd.Flags().GetBool("generate")
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
				violations, err := c.pushBudget(cmd, root, plan)
				if err != nil {
					return err
				}
				generated := ""
				if generate {
					if generated, err = c.generateCommitMessage(cmd, root); err != nil {
						return err
					}
				}
				return writePlanReport(cmd, plan, planReport{Violations: violations, Impact: c.pushImpact(cmd, root, plan), Generated: generated})
			}
			if generate {
				if message, err = c.generateCommitMessage(cmd, root); err != nil {
					return err
				}
				if err := c.confirmGenerated(cmd, "Generated commit message", message); err != nil {
					return err
				}
			}
			return c.useCase.Push().Run(cmd.Context(), root, message, newProgressRenderer(cmd.OutOrStdout()))
		},
	}
	cmd.Flags().StringP("message", "m", "", "Commit message for the push (required unless --generate)")
	addGenerateFlags(cmd, "commit message")
	cmd.MarkFlagsMutuallyExclusive("message", "generate")
	addDryRunFlags(cmd)
	return cmd
}
