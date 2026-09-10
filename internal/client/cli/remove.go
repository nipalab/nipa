package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (c *Cli) setupRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "remove <path> [...]",
		Short:         "Unmark files for the next push",
		Long:          "Remove the given files (or everything inside a directory) from the list of files marked for the next push. Use -a to unmark everything.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) == 0 {
				return fmt.Errorf("at least one path is required, or use -a to unmark everything")
			}
			if all {
				args = []string{""}
			}
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			return wc.Remove(cmd.Context(), args)
		},
	}
	cmd.Flags().BoolP("all", "a", false, "Unmark every staged file")
	return cmd
}
