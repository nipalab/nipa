package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
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
			if jsonRequested(cmd) {
				return output.WriteJSON(out, output.NewLog(entries))
			}
			if noPager || !isTTY(out) {
				return printLogPlain(out, entries, oneline)
			}
			return c.runLogInteractive(out, os.Stdin, entries, oneline)
		},
	}
	cmd.Flags().IntP("limit", "n", 0, "Limit the number of commits shown (0 = full history)")
	cmd.Flags().BoolP("oneline", "o", false, "Show one line per commit")
	cmd.Flags().Bool("no-pager", false, "Print log without the interactive pager")
	addJSONFlag(cmd)
	return cmd
}

// runLogInteractive puts the input terminal in raw mode, watches for window
// resize signals, and drives the scrollable pager until the user quits.
func (c *Cli) runLogInteractive(out io.Writer, in *os.File, entries []*serverDomain.CommitLogEntry, oneline bool) error {
	return runInteractivePager(out, in, logLines(entries, oneline, true), logFooter)
}

// runInteractivePager puts the input terminal in raw mode, watches for window
// resize signals, and drives the scrollable pager until the user quits.
func runInteractivePager(out io.Writer, in *os.File, lines []string, footer func(*logViewport) string) error {
	fd := int(in.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, state)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGWINCH)
	defer signal.Stop(sigs)
	return runViewportPager(out, in, lines, footer, func() (int, int) {
		w, h, err := term.GetSize(fd)
		if err != nil {
			return 80, 24
		}
		return w, h
	}, sigs)
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
	if cfg.Head != nil {
		start, err := detachedLogStart()
		if err != nil {
			return nil, err
		}
		if start != nil {
			opts = append(opts, usecase.WithCommitLogStart(*start))
		}
	}
	return c.useCase.Repo().Log(ctx, nipaUrl.Host, nipaUrl.Org, nipaUrl.Project, cfg.Branch, opts...)
}

// detachedLogStart resolves the pinned commit a detached working copy walks
// history from. A missing or unparsable pin falls back to the branch head.
func detachedLogStart() (*snow.ID, error) {
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return nil, err
	}
	lr := localrepo.NewLocalRepo()
	if err := lr.Init(root); err != nil {
		return nil, err
	}
	defer func() { _ = lr.Close() }()
	pin, err := lr.LoadCommit()
	if err != nil {
		return nil, err
	}
	if pin == nil || pin.CommitID == "" {
		return nil, nil
	}
	id, err := snow.ParseBase36(pin.CommitID)
	if err != nil {
		return nil, nil
	}
	return &id, nil
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
