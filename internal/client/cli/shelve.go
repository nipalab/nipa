package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

func (c *Cli) openShelve() (*usecase.Shelve, func(), error) {
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return nil, nil, err
	}
	lr := localrepo.NewLocalRepo()
	wc, err := usecase.NewWorkingCopy(lr, root)
	if err != nil {
		return nil, nil, err
	}
	if dl, ok := c.connector.(usecase.ContentDownloader); ok && c.useCase != nil {
		wc.WithDownloader(c.useCase.Auth(), dl)
	}
	return usecase.NewShelve(lr, wc, root), func() { _ = lr.Close() }, nil
}

func (c *Cli) setupShelveCmd() *cobra.Command {
	var name string
	var untracked bool
	cmd := &cobra.Command{
		Use:   "shelve [-m <name>] [-u] [-- <path>...]",
		Short: "Put local changes aside and restore the files",
		Long: "Store the changed files (or only the given paths) in a local shelf, then put them back to the synced version: " +
			"edited and deleted files are restored, new files removed and staged files unstaged. " +
			"Untracked files are taken when named or with -u. Shelves stay on this machine; bring one back with nipa shelve apply.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sh, cleanup, err := c.openShelve()
			if err != nil {
				return err
			}
			defer cleanup()
			shelf, err := sh.Create(cmd.Context(), usecase.ShelveOptions{Name: name, Paths: args, IncludeUntracked: untracked})
			if shelf != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "shelved %s as %q (%s)\n", fileCount(len(shelf.Entries)), shelf.Name, shelf.ID)
			}
			return err
		},
	}
	cmd.Flags().StringVarP(&name, "message", "m", "", "shelf name")
	cmd.Flags().BoolVarP(&untracked, "include-untracked", "u", false, "also shelve untracked files")
	cmd.AddCommand(c.setupShelveListCmd(), c.setupShelveShowCmd(), c.setupShelveApplyCmd(), c.setupShelveDropCmd())
	return cmd
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func (c *Cli) setupShelveListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List the shelves of this working copy, newest first",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sh, cleanup, err := c.openShelve()
			if err != nil {
				return err
			}
			defer cleanup()
			shelves, err := sh.List()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(shelves) == 0 {
				_, _ = fmt.Fprintln(out, "no shelves")
				return nil
			}
			for _, s := range shelves {
				_, _ = fmt.Fprintf(out, "%s  %s  %-12s  %-9s  %s\n", s.ID, s.CreatedAt.Local().Format("2006-01-02 15:04"), shelfWhere(s), fileCount(len(s.Entries)), s.Name)
			}
			return nil
		},
	}
}

func shelfWhere(s domain.Shelf) string {
	if s.Head != nil {
		return "tag " + s.Head.Name
	}
	return s.Branch
}

var shelfStatusLetters = map[string]string{domain.ShelfModified: "M", domain.ShelfAdded: "A", domain.ShelfDeleted: "D"}

func (c *Cli) setupShelveShowCmd() *cobra.Command {
	var patch bool
	cmd := &cobra.Command{
		Use:           "show <id>",
		Short:         "Show the files of a shelf",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sh, cleanup, err := c.openShelve()
			if err != nil {
				return err
			}
			defer cleanup()
			shelf, files, err := sh.Show(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "shelf %s %q\n", shelf.ID, shelf.Name)
			_, _ = fmt.Fprintf(out, "created %s on %s\n\n", shelf.CreatedAt.Local().Format("2006-01-02 15:04"), shelfWhere(*shelf))
			for _, e := range shelf.Entries {
				var notes []string
				if e.Staged {
					notes = append(notes, "staged")
				}
				if e.Untracked {
					notes = append(notes, "untracked")
				}
				if e.Locked {
					notes = append(notes, "locked")
				}
				suffix := ""
				if len(notes) > 0 {
					suffix = " (" + strings.Join(notes, ", ") + ")"
				}
				_, _ = fmt.Fprintf(out, "%s  %s%s\n", shelfStatusLetters[e.Status], e.Path, suffix)
			}
			if patch {
				writeShelfPatch(out, files)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&patch, "patch", "p", false, "print the text changes")
	return cmd
}

func writeShelfPatch(out io.Writer, files []diff.FileDiff) {
	_, _ = fmt.Fprintln(out)
	for _, line := range diff.Patch(files, diff.Options{}) {
		_, _ = fmt.Fprintln(out, line)
	}
}

func (c *Cli) setupShelveApplyCmd() *cobra.Command {
	var keep, abort, cont bool
	cmd := &cobra.Command{
		Use:   "apply <id> [--keep] | --continue | --abort",
		Short: "Bring a shelf back onto the working copy",
		Long: "Merge a shelf into the working copy: files changed since they were shelved are merged line by line. " +
			"Files with local changes are refused. Conflicts leave the apply waiting: edit the files and run " +
			"nipa shelve apply --continue, or put everything back with --abort. The shelf is deleted after a clean apply unless --keep.",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if abort && cont {
				return errors.New("--abort and --continue are mutually exclusive")
			}
			if (abort || cont) != (len(args) == 0) {
				return errors.New("give a shelf id, or --continue / --abort alone")
			}
			sh, cleanup, err := c.openShelve()
			if err != nil {
				return err
			}
			defer cleanup()
			out := cmd.OutOrStdout()
			switch {
			case abort:
				if err := sh.Abort(cmd.Context()); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, "apply aborted; the shelf was kept")
				return nil
			case cont:
				state, err := sh.Continue()
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "applied shelf %q\n", state.ShelfName)
				if !state.Keep {
					_, _ = fmt.Fprintln(out, "dropped the shelf")
				}
				return nil
			}
			res, err := sh.Apply(cmd.Context(), args[0], keep)
			if err != nil {
				return err
			}
			if len(res.Conflicts) > 0 {
				_, _ = fmt.Fprintf(out, "applying shelf %q stopped on conflicts:\n", res.Shelf.Name)
				for _, p := range res.Conflicts {
					_, _ = fmt.Fprintf(out, "  %s\n", p)
				}
				_, _ = fmt.Fprintln(out, "edit the files, then run nipa shelve apply --continue (or --abort)")
				return nil
			}
			_, _ = fmt.Fprintf(out, "applied shelf %q\n", res.Shelf.Name)
			if res.Dropped {
				_, _ = fmt.Fprintln(out, "dropped the shelf")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep", false, "keep the shelf after applying it")
	cmd.Flags().BoolVar(&abort, "abort", false, "stop a waiting apply and put the files back")
	cmd.Flags().BoolVar(&cont, "continue", false, "finish a waiting apply once the conflicts are edited")
	return cmd
}

func (c *Cli) setupShelveDropCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "drop <id>",
		Short:         "Delete a shelf",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sh, cleanup, err := c.openShelve()
			if err != nil {
				return err
			}
			defer cleanup()
			if err := sh.Drop(args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dropped shelf %s\n", args[0])
			return nil
		},
	}
}
