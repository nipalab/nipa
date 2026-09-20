package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

// ErrExitCode is returned when differences were found and the caller asked
// for exit-code semantics.
var ErrExitCode = errors.New("differences found")

type diffMode int

const (
	diffModePatch diffMode = iota
	diffModeStat
	diffModeNumStat
	diffModeShortStat
	diffModeNameOnly
	diffModeNameStatus
	diffModeRaw
	diffModeSummary
)

const (
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
)

func (c *Cli) setupDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "diff [--] [<path>...]",
		Short:         "Show working-copy changes",
		Long:          "Show changes between the working tree and the last synced snapshot as a unified patch. With paths after `--`, only those paths are compared. Untracked files are not shown (see nipa status); staged new files are. Use -U to change the context size, --stat/--numstat/--shortstat, --name-only/--name-status or --raw for other formats, --staged for what the next push would upload, --exit-code to fail when there are differences, and --no-pager/--no-color to disable the pager or colors.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runDiff(cmd, args)
		},
	}
	cmd.Flags().IntP("unified", "U", diff.DefaultContext, "Show <n> lines of context")
	cmd.Flags().Int("inter-hunk-context", 0, "Fuse hunks separated by fewer than <n> lines")
	cmd.Flags().BoolP("patch", "p", false, "Show patch output (default)")
	cmd.Flags().Bool("stat", false, "Show a per-file summary instead of patches")
	cmd.Flags().Bool("numstat", false, "Show numeric per-file changes")
	cmd.Flags().Bool("shortstat", false, "Show only the summary line")
	cmd.Flags().Bool("name-only", false, "Show only changed file paths")
	cmd.Flags().Bool("name-status", false, "Show changed paths with A/M/D status")
	cmd.Flags().Bool("raw", false, "Show the raw change format")
	cmd.Flags().Bool("summary", false, "Show mode changes")
	cmd.Flags().Bool("staged", false, "Show only staged changes")
	cmd.Flags().Bool("cached", false, "Alias for --staged")
	cmd.Flags().BoolP("reverse", "R", false, "Swap the compared sides")
	cmd.Flags().BoolP("text", "a", false, "Treat binary files as text")
	cmd.Flags().String("diff-filter", "", "Limit to statuses (A, M, D)")
	cmd.Flags().Bool("no-prefix", false, "Do not show a/ and b/ prefixes")
	cmd.Flags().String("src-prefix", "", "Use <prefix> instead of a/")
	cmd.Flags().String("dst-prefix", "", "Use <prefix> instead of b/")
	cmd.Flags().String("line-prefix", "", "Prepend <prefix> to every output line")
	cmd.Flags().String("output-indicator-old", "", "Indicator for removed lines (default -)")
	cmd.Flags().String("output-indicator-new", "", "Indicator for added lines (default +)")
	cmd.Flags().String("output-indicator-context", "", "Indicator for context lines (default space)")
	cmd.Flags().String("output", "", "Write output to <file>")
	cmd.Flags().Bool("exit-code", false, "Exit with status 1 when there are differences")
	cmd.Flags().Bool("quiet", false, "Suppress output and exit with status 1 on differences")
	cmd.Flags().String("color", "auto", "Color output: always, auto or never")
	cmd.Flags().Bool("no-color", false, "Disable colors")
	cmd.Flags().Bool("no-pager", false, "Print without the interactive pager")
	return cmd
}

func (c *Cli) runDiff(cmd *cobra.Command, args []string) error {
	mode, err := diffOutputMode(cmd)
	if err != nil {
		return err
	}
	compareOpts, err := diffCompareOptions(cmd, args)
	if err != nil {
		return err
	}
	renderOpts, err := diffRenderOptions(cmd)
	if err != nil {
		return err
	}
	colorFlag, _ := cmd.Flags().GetString("color")
	if colorFlag != "always" && colorFlag != "auto" && colorFlag != "never" {
		return fmt.Errorf("--color must be always, auto or never")
	}
	quiet, _ := cmd.Flags().GetBool("quiet")
	outputPath, _ := cmd.Flags().GetString("output")
	exitCode, _ := cmd.Flags().GetBool("exit-code")
	noPager, _ := cmd.Flags().GetBool("no-pager")
	noColor, _ := cmd.Flags().GetBool("no-color")

	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return err
	}
	files, err := c.useCase.Diff().Run(cmd.Context(), root, compareOpts)
	if err != nil {
		return err
	}
	if quiet {
		return exitCodeResult(files, true)
	}

	lines := renderDiff(files, mode, renderOpts)
	out := cmd.OutOrStdout()
	if outputPath != "" {
		if err := writeDiffFile(outputPath, lines); err != nil {
			return err
		}
		return exitCodeResult(files, exitCode)
	}
	if diffUseColor(out, noColor, colorFlag) {
		lines = colorizeDiffLines(lines, mode)
	}
	if mode == diffModePatch && !noPager && isTTY(out) {
		if err := runInteractivePager(out, os.Stdin, lines, diffFooter(len(files))); err != nil {
			return err
		}
		return exitCodeResult(files, exitCode)
	}
	if err := writeDiffLines(out, lines); err != nil {
		return err
	}
	return exitCodeResult(files, exitCode)
}

func exitCodeResult(files []diff.FileDiff, exitCode bool) error {
	if exitCode && len(files) > 0 {
		return ErrExitCode
	}
	return nil
}

func diffOutputMode(cmd *cobra.Command) (diffMode, error) {
	formats := []struct {
		name string
		mode diffMode
	}{
		{"stat", diffModeStat},
		{"numstat", diffModeNumStat},
		{"shortstat", diffModeShortStat},
		{"name-only", diffModeNameOnly},
		{"name-status", diffModeNameStatus},
		{"raw", diffModeRaw},
		{"summary", diffModeSummary},
	}
	mode := diffModePatch
	count := 0
	for _, f := range formats {
		if on, _ := cmd.Flags().GetBool(f.name); on {
			mode = f.mode
			count++
		}
	}
	if count > 1 {
		return mode, fmt.Errorf("only one output format may be given")
	}
	if patch, _ := cmd.Flags().GetBool("patch"); patch && count > 0 {
		return mode, fmt.Errorf("--patch cannot be combined with another output format")
	}
	return mode, nil
}

func diffCompareOptions(cmd *cobra.Command, args []string) (usecase.DiffOptions, error) {
	staged, _ := cmd.Flags().GetBool("staged")
	cached, _ := cmd.Flags().GetBool("cached")
	reverse, _ := cmd.Flags().GetBool("reverse")
	rawFilter, _ := cmd.Flags().GetString("diff-filter")
	filter, err := diffStatusFilter(rawFilter)
	if err != nil {
		return usecase.DiffOptions{}, err
	}
	return usecase.DiffOptions{
		Staged:  staged || cached,
		Paths:   args,
		Filter:  filter,
		Reverse: reverse,
	}, nil
}

func diffStatusFilter(s string) (map[diff.Status]bool, error) {
	if s == "" {
		return nil, nil
	}
	out := make(map[diff.Status]bool)
	for _, r := range s {
		switch r {
		case 'A':
			out[diff.Added] = true
		case 'M':
			out[diff.Modified] = true
		case 'D':
			out[diff.Deleted] = true
		default:
			return nil, fmt.Errorf("unsupported --diff-filter status %q (use A, M or D)", string(r))
		}
	}
	return out, nil
}

func diffRenderOptions(cmd *cobra.Command) (diff.Options, error) {
	context, _ := cmd.Flags().GetInt("unified")
	if context < 0 {
		return diff.Options{}, fmt.Errorf("--unified must not be negative")
	}
	interHunk, _ := cmd.Flags().GetInt("inter-hunk-context")
	if interHunk < 0 {
		return diff.Options{}, fmt.Errorf("--inter-hunk-context must not be negative")
	}
	noPrefix, _ := cmd.Flags().GetBool("no-prefix")
	srcPrefix, _ := cmd.Flags().GetString("src-prefix")
	dstPrefix, _ := cmd.Flags().GetString("dst-prefix")
	linePrefix, _ := cmd.Flags().GetString("line-prefix")
	text, _ := cmd.Flags().GetBool("text")

	indicators := make([]string, 0, 3)
	for _, name := range []string{"output-indicator-old", "output-indicator-new", "output-indicator-context"} {
		value, _ := cmd.Flags().GetString(name)
		if value != "" && len(value) != 1 {
			return diff.Options{}, fmt.Errorf("--%s expects a single character", name)
		}
		indicators = append(indicators, value)
	}
	return diff.Options{
		Context:          context,
		InterHunkContext: interHunk,
		NoPrefix:         noPrefix,
		SrcPrefix:        srcPrefix,
		DstPrefix:        dstPrefix,
		LinePrefix:       linePrefix,
		Text:             text,
		OldIndicator:     indicators[0],
		NewIndicator:     indicators[1],
		ContextIndicator: indicators[2],
	}, nil
}

func renderDiff(files []diff.FileDiff, mode diffMode, opts diff.Options) []string {
	switch mode {
	case diffModeStat:
		return diff.Stat(files, opts)
	case diffModeNumStat:
		return diff.NumStat(files, opts)
	case diffModeShortStat:
		return diff.ShortStat(files, opts)
	case diffModeNameOnly:
		return diff.NameOnly(files, opts)
	case diffModeNameStatus:
		return diff.NameStatus(files, opts)
	case diffModeRaw:
		return diff.Raw(files, opts)
	case diffModeSummary:
		return diff.Summary(files, opts)
	default:
		return diff.Patch(files, opts)
	}
}

func diffUseColor(out io.Writer, noColor bool, colorFlag string) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	switch colorFlag {
	case "always":
		return true
	case "never":
		return false
	default:
		return isTTY(out)
	}
}

func colorizeDiffLines(lines []string, mode diffMode) []string {
	for i, line := range lines {
		switch mode {
		case diffModePatch:
			lines[i] = colorizePatchLine(line)
		case diffModeStat:
			lines[i] = colorizeStatLine(line)
		case diffModeNameStatus:
			lines[i] = colorizeNameStatusLine(line)
		}
	}
	return lines
}

func colorizePatchLine(line string) string {
	switch {
	case strings.HasPrefix(line, "@@"):
		return ansiCyan + line + ansiColorReset
	case strings.HasPrefix(line, "diff --nipa"),
		strings.HasPrefix(line, "--- "),
		strings.HasPrefix(line, "+++ "),
		strings.HasPrefix(line, "old mode"),
		strings.HasPrefix(line, "new mode"),
		strings.HasPrefix(line, "new file mode"),
		strings.HasPrefix(line, "deleted file mode"),
		strings.HasPrefix(line, "Binary files"):
		return ansiBold + line + ansiColorReset
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return ansiGreen + line + ansiColorReset
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return ansiRed + line + ansiColorReset
	default:
		return line
	}
}

func colorizeStatLine(line string) string {
	sep := strings.Index(line, " | ")
	if sep < 0 {
		return line
	}
	rest := line[sep+3:]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return line
	}
	graph := rest[sp+1:]
	if graph == "" || strings.Trim(graph, "+-") != "" {
		return line
	}
	head, count := line[:sep+3], rest[:sp]
	if i := strings.IndexByte(graph, '-'); i < 0 {
		return head + count + " " + ansiGreen + graph + ansiColorReset
	} else if i == 0 {
		return head + count + " " + ansiRed + graph + ansiColorReset
	} else {
		return head + count + " " + ansiGreen + graph[:i] + ansiColorReset + ansiRed + graph[i:] + ansiColorReset
	}
}

func colorizeNameStatusLine(line string) string {
	tab := strings.IndexByte(line, '\t')
	if tab <= 0 {
		return line
	}
	switch line[:tab] {
	case "A":
		return ansiGreen + line + ansiColorReset
	case "M":
		return ansiColorYellow + line + ansiColorReset
	case "D":
		return ansiRed + line + ansiColorReset
	}
	return line
}

func writeDiffLines(out io.Writer, lines []string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

func writeDiffFile(path string, lines []string) error {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
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
