package cli

import (
	"fmt"
	"os"

	clientDiff "github.com/nipalab/nipa/internal/client/diff"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "diff",
		Short:         "Show working-copy changes",
		Long:          "Show changes between the working copy and the last synced tree. Tracked modifications and staged new files are shown as a unified patch; use -U to change the context size, or --no-pager to print without the interactive pager. Untracked files are listed by nipa status.",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			unified, _ := cmd.Flags().GetInt("unified")
			noPager, _ := cmd.Flags().GetBool("no-pager")
			res, err := c.useCase.Diff().Run(cmd.Context(), root, args)
			if err != nil {
				return err
			}
			var lines []string
			opts := clientDiff.Options{Context: unified}
			for _, f := range res.Files {
				if f.OldUnavailable {
					lines = append(lines,
						clientDiff.HeaderLine(f.Change, opts),
						"old content not available locally (run `nipa update` to fetch it)",
					)
					continue
				}
				lines = append(lines, clientDiff.FilePatch(f.Change, f.Old, f.New, opts)...)
			}
			if len(lines) == 0 {
				return nil
			}
			out := cmd.OutOrStdout()
			if noPager || !isTTY(out) {
				for _, line := range lines {
					if _, err := fmt.Fprintln(out, line); err != nil {
						return err
					}
				}
				return nil
			}
			return runInteractivePager(out, os.Stdin, lines, diffFooter(len(res.Files)))
		},
	}
	cmd.Flags().IntP("unified", "U", 0, "Show <n> lines of context (default 3)")
	cmd.Flags().Bool("no-pager", false, "Print diff without the interactive pager")
	return cmd
}

func diffFooter(files int) func(*logViewport) string {
	return func(vp *logViewport) string {
		if vp.total() == 0 {
			return "nipa diff: no changes"
		}
		last := vp.top + len(vp.visibleLines())
		return fmt.Sprintf("nipa diff: %d files, lines %d-%d of %d (q to quit)", files, vp.top+1, last, vp.total())
	}
}
