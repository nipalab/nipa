package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func (c *Cli) setupGCCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "gc",
		Short:         "Remove cached objects the working copy no longer references",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			lr := localrepo.NewLocalRepo()
			defer func() { _ = lr.Close() }()
			res, err := usecase.NewGC(lr).Run(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.Skipped {
				_, _ = fmt.Fprintf(out, "skipped: %s\n", res.SkipReason)
				return nil
			}
			_, _ = fmt.Fprintf(out, "removed %d object(s), freed %s\n", res.Objects, humanBytes(res.Bytes))
			return nil
		},
	}
}
