package cli

import (
	"github.com/spf13/cobra"
)

func (c *Cli) setupRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "restore <path> [...]",
		Short:         "Discard unpushed changes to files or directories",
		Long:          "Restore the given files (or everything inside directories) to the last synced tree, discarding local modifications and re-creating deleted files. This cannot be undone. Paths that are staged for the next push are refused: push or reset them first.",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			return wc.Restore(cmd.Context(), args)
		},
	}
	return cmd
}
