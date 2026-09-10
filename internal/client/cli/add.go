package cli

import (
	"github.com/spf13/cobra"
)

func (c *Cli) setupAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "add <path> [...]",
		Short:         "Mark files to be pushed",
		Long:          "Mark the given files (or everything inside directories) for the next push. Paths are relative to the repository root.",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			return wc.Add(cmd.Context(), args)
		},
	}
	return cmd
}
