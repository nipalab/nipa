package cli

import (
	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/usecase"
)

func (c *Cli) setupAddCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "add <path> [...]",
		Short:         "Mark files to be pushed",
		Long:          "Mark the given files (or everything inside directories) for the next push. Missing paths that are still tracked are marked as deletions. Paths are relative to the repository root. Files matching the ignore rules in the root .nipaignore file or the local .nipa/ignore file are skipped unless -f is given.",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			return wc.Add(cmd.Context(), args, usecase.AddOptions{Force: force})
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "stage ignored files anyway")
	return cmd
}
