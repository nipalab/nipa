package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	clientconfig "github.com/nipalab/nipa/internal/client/config"
	clientDiff "github.com/nipalab/nipa/internal/client/diff"
	"github.com/nipalab/nipa/internal/client/difftool"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

func (c *Cli) setupDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "diff [rev1] [rev2]",
		Short:         "Show working-copy and revision changes",
		Long:          "Show changes as a unified patch. With no arguments, compares the working copy against the last synced tree (tracked modifications and staged new files; untracked files are listed by nipa status). With one revision, compares the revision against the working copy; with two, compares the first revision against the second. A revision is a branch name, a commit ID, or a commit hash. Use -U to change the context size, --stat for a per-file summary, --name-only/--name-status for path lists, or --no-pager to print without the interactive pager. Use --tool '<cmd>' or --external to open each changed file in an external app instead ($LOCAL/$REMOTE placeholders, configured via NIPA_DIFF_TOOL or diffTool in the user config).",
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
			noColor, _ := cmd.Flags().GetBool("no-color")
			stat, _ := cmd.Flags().GetBool("stat")
			nameOnly, _ := cmd.Flags().GetBool("name-only")
			nameStatus, _ := cmd.Flags().GetBool("name-status")
			toolFlag, _ := cmd.Flags().GetString("tool")
			external, _ := cmd.Flags().GetBool("external")
			modes := 0
			for _, on := range []bool{stat, nameOnly, nameStatus} {
				if on {
					modes++
				}
			}
			if modes > 1 {
				return fmt.Errorf("only one of --stat, --name-only, --name-status may be given")
			}
			if toolFlag != "" && external {
				return fmt.Errorf("only one of --tool, --external may be given")
			}
			if (toolFlag != "" || external) && modes > 0 {
				return fmt.Errorf("--tool and --external cannot be combined with --stat, --name-only, --name-status")
			}
			if toolFlag != "" || external {
				return c.runExternalDiff(cmd, root, args, toolFlag)
			}
			res, err := c.useCase.Diff().Run(cmd.Context(), root, args)
			if err != nil {
				return err
			}
			var lines []string
			switch {
			case stat:
				lines = diffStatLines(res.Files)
			case nameOnly:
				lines = diffNameLines(res.Files, false)
			case nameStatus:
				lines = diffNameLines(res.Files, true)
			default:
				lines = diffPatchLines(res.Files, unified)
			}
			if len(lines) == 0 {
				return nil
			}
			out := cmd.OutOrStdout()
			if useColor(out, noColor) {
				for i, line := range lines {
					lines[i] = colorizeDiffLine(line, stat)
				}
			}
			if noPager || !isTTY(out) || stat || nameOnly || nameStatus {
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
	cmd.Flags().Bool("no-color", false, "Print diff without colors")
	cmd.Flags().Bool("stat", false, "Show a per-file summary instead of patches")
	cmd.Flags().Bool("name-only", false, "Show only changed file paths")
	cmd.Flags().Bool("name-status", false, "Show changed file paths with A/M/D status")
	cmd.Flags().String("tool", "", "Show each changed file in an external app, e.g. --tool 'code --diff $LOCAL $REMOTE'")
	cmd.Flags().Bool("external", false, "Show each changed file in the configured external diff tool")
	return cmd
}

func (c *Cli) runExternalDiff(cmd *cobra.Command, root string, args []string, toolFlag string) error {
	tmplStr, err := clientconfig.ResolveDiffTool(toolFlag)
	if err != nil {
		return err
	}
	if tmplStr == "" {
		path, _ := clientconfig.Path()
		return fmt.Errorf("no external diff tool configured (use --tool, set %s, or set diffTool in %s)", clientconfig.EnvDiffTool, path)
	}
	tmpl, err := difftool.Parse(tmplStr)
	if err != nil {
		return err
	}
	res, err := c.useCase.Diff().Run(cmd.Context(), root, args, usecase.WithBinaryContent())
	if err != nil {
		return err
	}
	files := make([]difftool.File, 0, len(res.Files))
	for _, f := range res.Files {
		df := difftool.File{Path: f.Change.Path, Status: f.Change.Status.String()}
		switch f.Change.Status {
		case clientDiff.Added:
			df.New = f.New
			df.Unavailable = f.NewUnavailable
		case clientDiff.Deleted:
			df.Old = f.Old
			df.Unavailable = f.OldUnavailable
		default:
			df.Old, df.New = f.Old, f.New
			df.Unavailable = f.OldUnavailable || f.NewUnavailable
		}
		files = append(files, df)
	}
	tree, pairs, skipped, err := difftool.Materialize(files)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tree.Dir)
	errW := cmd.ErrOrStderr()
	for _, s := range skipped {
		fmt.Fprintf(errW, "warning: skipping '%s': content not available locally\n", s)
	}
	if len(pairs) == 0 {
		return nil
	}
	return difftool.Run(cmd.Context(), c.externalRunner, tmpl, tree, pairs, errW)
}

func diffPatchLines(files []usecase.DiffFile, unified int) []string {
	var lines []string
	opts := clientDiff.Options{Context: unified}
	for _, f := range files {
		if f.OldUnavailable || f.NewUnavailable {
			lines = append(lines,
				clientDiff.HeaderLine(f.Change, opts),
				"content not available locally (run `nipa update` to fetch it)",
			)
			continue
		}
		lines = append(lines, clientDiff.FilePatch(f.Change, f.Old, f.New, opts)...)
	}
	return lines
}

func diffNameLines(files []usecase.DiffFile, withStatus bool) []string {
	lines := make([]string, 0, len(files))
	for _, f := range files {
		if withStatus {
			lines = append(lines, f.Change.Status.String()+"\t"+f.Change.Path)
		} else {
			lines = append(lines, f.Change.Path)
		}
	}
	return lines
}

func diffStatLines(files []usecase.DiffFile) []string {
	width := 0
	for _, f := range files {
		width = max(width, len(f.Change.Path))
	}
	var lines []string
	added, removed := 0, 0
	for _, f := range files {
		if f.Change.Old.IsBinary || f.Change.New.IsBinary {
			lines = append(lines, fmt.Sprintf(" %-*s | Bin %d -> %d bytes",
				width, f.Change.Path, f.Change.Old.SizeBytes, f.Change.New.SizeBytes))
			continue
		}
		a, r := clientDiff.Stat(f.Old, f.New)
		added += a
		removed += r
		if a+r == 0 {
			lines = append(lines, fmt.Sprintf(" %-*s | 0", width, f.Change.Path))
			continue
		}
		lines = append(lines, fmt.Sprintf(" %-*s | %d %s",
			width, f.Change.Path, a+r, strings.Repeat("+", a)+strings.Repeat("-", r)))
	}
	if len(files) > 0 {
		lines = append(lines, fmt.Sprintf(" %d %s, %d %s, %d %s",
			len(files), plural(len(files), "file changed", "files changed"),
			added, plural(added, "insertion(+)", "insertions(+)"),
			removed, plural(removed, "deletion(-)", "deletions(-)")))
	}
	return lines
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func useColor(out io.Writer, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY(out)
}

func colorizeDiffLine(line string, stat bool) string {
	if stat {
		return colorizeStatLine(line)
	}
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return "\x1b[32m" + line + "\x1b[m"
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return "\x1b[31m" + line + "\x1b[m"
	case strings.HasPrefix(line, "@@"):
		return "\x1b[36m" + line + "\x1b[m"
	case strings.HasPrefix(line, "diff --nipa"),
		strings.HasPrefix(line, "---"),
		strings.HasPrefix(line, "+++"):
		return "\x1b[1m" + line + "\x1b[m"
	default:
		return line
	}
}

func colorizeStatLine(line string) string {
	sep := strings.LastIndex(line, " | ")
	if sep < 0 {
		return line
	}
	head, rest := line[:sep+3], line[sep+3:]
	// rest is "<total> <graph>", "0", or "Bin ..."; only the graph of
	// +/- runs gets colored.
	sp := strings.Index(rest, " ")
	if sp < 0 {
		return line
	}
	num, graph := rest[:sp], rest[sp+1:]
	if graph == "" {
		return line
	}
	for i := 0; i < len(graph); i++ {
		if graph[i] != '+' && graph[i] != '-' {
			return line
		}
	}
	var colored strings.Builder
	if k := strings.Index(graph, "-"); k < 0 {
		colored.WriteString("\x1b[32m" + graph + "\x1b[m")
	} else {
		if k > 0 {
			colored.WriteString("\x1b[32m" + graph[:k] + "\x1b[m")
		}
		colored.WriteString("\x1b[31m" + graph[k:] + "\x1b[m")
	}
	return head + num + " " + colored.String()
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
