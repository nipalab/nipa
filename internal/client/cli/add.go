package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/usecase"
)

func (c *Cli) setupAddCmd() *cobra.Command {
	var force, noProvenance bool
	cmd := &cobra.Command{
		Use:           "add <path> [...]",
		Short:         "Mark files to be pushed",
		Long:          "Mark the given files (or everything inside directories) for the next push. Missing paths that are still tracked are marked as deletions, and tracked files that are unchanged are skipped. Paths are relative to the repository root. Files matching the ignore rules in the root .nipaignore file or the local .nipa/ignore file are skipped unless -f is given. When the repository has a .nipaprovenance.yml, new PNG/JPEG files that declare their generator (Automatic1111 or ComfyUI text chunks, C2PA labels) are recorded in it, records follow renamed files with the same content, and staged deletions drop their records; --no-provenance skips this.",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			rep, err := wc.AddWithReport(cmd.Context(), args, usecase.AddOptions{Force: force, NoProvenance: noProvenance})
			if err != nil {
				return err
			}
			if rep.Changed() {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), rep.String())
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "stage ignored files anyway")
	cmd.Flags().BoolVar(&noProvenance, "no-provenance", false, "do not record provenance for the staged files")
	return cmd
}
