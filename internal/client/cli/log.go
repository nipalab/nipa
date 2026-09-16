package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (c *Cli) setupLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "log",
		Short:         "Show commit history",
		Long:          "Show the commit history of the current branch. Displays an interactive, scrollable list when stdout is a terminal; use -n to limit, --oneline for one line per commit, or --no-pager to print without the interactive pager.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := c.loadConfig()
			if err != nil {
				return err
			}
			limit, _ := cmd.Flags().GetInt("limit")
			oneline, _ := cmd.Flags().GetBool("oneline")
			noPager, _ := cmd.Flags().GetBool("no-pager")
			entries, err := c.fetchLog(cmd, cfg, limit)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if noPager || !isTTY(out) {
				return printLogPlain(out, entries, oneline)
			}
			fd := int(os.Stdin.Fd())
			state, err := term.MakeRaw(fd)
			if err != nil {
				return err
			}
			defer term.Restore(fd, state)
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGWINCH)
			defer signal.Stop(sigs)
			return runLogPager(out, os.Stdin, entries, oneline, func() (int, int) {
				w, h, err := term.GetSize(fd)
				if err != nil {
					return 80, 24
				}
				return w, h
			}, sigs)
		},
	}
	cmd.Flags().IntP("limit", "n", 0, "Limit the number of commits shown (0 = full history)")
	cmd.Flags().BoolP("oneline", "o", false, "Show one line per commit")
	cmd.Flags().Bool("no-pager", false, "Print log without the interactive pager")
	return cmd
}

func (c *Cli) fetchLog(cmd *cobra.Command, cfg *domain.Config, limit int) ([]*serverDomain.CommitLogEntry, error) {
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := c.connector.Connect(ctx, nipaUrl.Host); err != nil {
		return nil, err
	}
	opts := []usecase.CommitLogOption{}
	if limit > 0 {
		opts = append(opts, usecase.WithCommitLogMax(limit))
	}
	return c.useCase.Repo().Log(ctx, nipaUrl.Host, nipaUrl.Org, nipaUrl.Project, cfg.Branch, opts...)
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
