package cli

import (
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

func (c *Cli) setupStatusCmd() *cobra.Command {
	var noCache bool
	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show the working copy status",
		Long:          "Show files marked for the next push (A), marked as deleted (D), modified but not marked (M), untracked (?) and missing (!).",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			st, err := wc.Status(cmd.Context(), usecase.StatusOptions{NoCache: noCache})
			if err != nil {
				return err
			}
			writeStatus(cmd, st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "Rehash every tracked file instead of trusting the stat cache")
	return cmd
}

func writeStatus(cmd *cobra.Command, st *domain.Status) {
	for _, p := range st.Staged {
		cmd.Printf("A  %s\n", p)
	}
	for _, p := range st.Deleted {
		cmd.Printf("D  %s\n", p)
	}
	for _, p := range st.Modified {
		cmd.Printf("M  %s\n", p)
	}
	for _, p := range st.Untracked {
		cmd.Printf("?  %s\n", p)
	}
	for _, p := range st.Missing {
		cmd.Printf("!  %s\n", p)
	}
	for _, p := range st.Conflicts {
		cmd.Printf("C  %s\n", p)
	}
}
