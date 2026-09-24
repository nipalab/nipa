package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	clientconfig "github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/difftool"
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
	diffModeNameOnly
	diffModeNameStatus
)

const (
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
)

func (c *Cli) setupDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "diff [<rev1> [<rev2>]] [--] [<path>...]",
		Short:         "Show working-copy and revision changes",
		Long:          "Show changes as a unified patch. With no revisions, the working tree is compared against the last synced snapshot, fully offline (staged new files count as additions; untracked files are listed by nipa status). With one revision, the revision's tree is compared against the working tree; with two, the first revision's tree is compared against the second. A revision is a branch name, a base36 commit ID (as printed by nipa log) or HEAD/@ (the locally pinned commit, falling back to the configured branch); <a>..<b> compares the two endpoints and <a>...<b> compares their merge base against <b>. Renames are detected automatically. Use -U to change the context size, --stat/--name-only/--name-status for other formats, --staged for what the next push would upload, -w/-b to ignore whitespace, --ext-diff to open each changed file in the configured external tool, --exit-code to fail when there are differences, and --no-cache to re-read every working file instead of trusting the stat cache.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runDiff(cmd, args)
		},
	}
	cmd.Flags().IntP("unified", "U", diff.DefaultContext, "Show <n> lines of context")
	cmd.Flags().Bool("stat", false, "Show a per-file summary instead of patches")
	cmd.Flags().Bool("name-only", false, "Show only changed file paths")
	cmd.Flags().Bool("name-status", false, "Show changed paths with A/M/D/R status")
	cmd.Flags().Bool("staged", false, "Show only staged changes")
	cmd.Flags().BoolP("ignore-all-space", "w", false, "Ignore all whitespace when comparing lines")
	cmd.Flags().BoolP("ignore-space-change", "b", false, "Ignore changes in the amount of whitespace")
	cmd.Flags().Bool("no-color", false, "Disable colors")
	cmd.Flags().Bool("no-pager", false, "Print without the interactive pager")
	cmd.Flags().Bool("ext-diff", false, "Use the configured external diff command")
	cmd.Flags().Bool("exit-code", false, "Exit with status 1 when there are differences")
	cmd.Flags().Bool("no-cache", false, "Read every working file instead of trusting the stat cache")
	return cmd
}

func (c *Cli) runDiff(cmd *cobra.Command, args []string) error {
	mode, err := diffOutputMode(cmd)
	if err != nil {
		return err
	}
	revs, paths, err := splitDiffArgs(cmd, args)
	if err != nil {
		return err
	}
	revs, mergeBase, err := expandRevisionRange(revs)
	if err != nil {
		return err
	}
	if mergeBase && len(revs) != 2 {
		return fmt.Errorf("a three-dot revision range requires two revisions")
	}
	compareOpts, err := diffCompareOptions(cmd, revs, paths, mergeBase)
	if err != nil {
		return err
	}
	renderOpts, err := diffRenderOptions(cmd)
	if err != nil {
		return err
	}
	useExternal, externalCommand, err := externalDiffOptions(cmd, mode)
	if err != nil {
		return err
	}
	if useExternal {
		compareOpts.Binary = true
	}
	noPager, _ := cmd.Flags().GetBool("no-pager")
	noColor, _ := cmd.Flags().GetBool("no-color")
	exitCode, _ := cmd.Flags().GetBool("exit-code")

	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return err
	}
	files, err := c.useCase.Diff().Run(cmd.Context(), root, revs, compareOpts)
	if err != nil {
		return err
	}
	files = diff.FilterIgnored(files, renderOpts)
	if useExternal {
		if err := runExternalDiff(cmd, externalCommand, files); err != nil {
			return err
		}
		return exitCodeResult(files, exitCode)
	}

	lines := renderDiff(files, mode, renderOpts)
	out := cmd.OutOrStdout()
	if diffUseColor(out, noColor) {
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
		{"name-only", diffModeNameOnly},
		{"name-status", diffModeNameStatus},
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
	return mode, nil
}

func splitDiffArgs(cmd *cobra.Command, args []string) ([]string, []string, error) {
	dash := cmd.Flags().ArgsLenAtDash()
	if dash < 0 {
		if len(args) > 2 {
			return nil, nil, fmt.Errorf("too many revisions (expected at most two; use -- before paths)")
		}
		return args, nil, nil
	}
	revs, paths := args[:dash], args[dash:]
	if len(revs) > 2 {
		return nil, nil, fmt.Errorf("too many revisions (expected at most two)")
	}
	return revs, paths, nil
}

// expandRevisionRange splits <a>..<b> and <a>...<b> into two revisions. The
// three-dot form selects the merge base as the old side.
func expandRevisionRange(revs []string) ([]string, bool, error) {
	if len(revs) != 1 {
		return revs, false, nil
	}
	token := revs[0]
	if a, b, ok := strings.Cut(token, "..."); ok {
		if a == "" || b == "" {
			return nil, false, fmt.Errorf("a revision range must be <a>...<b>")
		}
		return []string{a, b}, true, nil
	}
	if a, b, ok := strings.Cut(token, ".."); ok {
		if a == "" || b == "" {
			return nil, false, fmt.Errorf("a revision range must be <a>..<b>")
		}
		return []string{a, b}, false, nil
	}
	return revs, false, nil
}

func diffCompareOptions(cmd *cobra.Command, revs, paths []string, mergeBase bool) (usecase.DiffOptions, error) {
	staged, _ := cmd.Flags().GetBool("staged")
	if len(revs) > 0 && staged {
		return usecase.DiffOptions{}, fmt.Errorf("--staged cannot be combined with revisions")
	}
	noCache, _ := cmd.Flags().GetBool("no-cache")
	return usecase.DiffOptions{
		Staged:    staged,
		Paths:     paths,
		MergeBase: mergeBase,
		NoCache:   noCache,
	}, nil
}

func diffRenderOptions(cmd *cobra.Command) (diff.Options, error) {
	context, _ := cmd.Flags().GetInt("unified")
	if context < 0 {
		return diff.Options{}, fmt.Errorf("--unified must not be negative")
	}
	allSpace, _ := cmd.Flags().GetBool("ignore-all-space")
	spaceChange, _ := cmd.Flags().GetBool("ignore-space-change")
	return diff.Options{
		Context:           context,
		IgnoreAllSpace:    allSpace,
		IgnoreSpaceChange: spaceChange,
	}, nil
}

func externalDiffOptions(cmd *cobra.Command, mode diffMode) (bool, string, error) {
	force, _ := cmd.Flags().GetBool("ext-diff")
	if !force {
		return false, "", nil
	}
	if mode != diffModePatch {
		return false, "", fmt.Errorf("--ext-diff cannot be combined with another output format")
	}
	command, err := clientconfig.ResolveDiffExternal()
	if err != nil {
		return false, "", err
	}
	if command == "" {
		path, _ := clientconfig.Path()
		return false, "", fmt.Errorf("no external diff command configured (set %s or diffExternal in %s)", clientconfig.EnvDiffExternal, path)
	}
	return true, command, nil
}

func runExternalDiff(cmd *cobra.Command, command string, files []diff.FileDiff) error {
	pairs := make([]difftool.FilePair, 0, len(files))
	errW := cmd.ErrOrStderr()
	for _, f := range files {
		if f.OldUnavailable || f.NewUnavailable {
			_, _ = fmt.Fprintf(errW, "warning: skipping '%s': content not available locally\n", f.Change.Path)
			continue
		}
		pairs = append(pairs, externalPair(f))
	}
	return difftool.Run(cmd.Context(), command, pairs, cmd.OutOrStdout(), errW)
}

func externalPair(f diff.FileDiff) difftool.FilePair {
	c := f.Change
	pair := difftool.FilePair{
		Path:    c.Path,
		Old:     f.Old,
		New:     f.New,
		OldHash: c.Old.Hash.String(),
		NewHash: c.New.Hash.String(),
		OldMode: diff.ModeString(c.Old.Mode),
		NewMode: diff.ModeString(c.New.Mode),
	}
	switch c.Status {
	case diff.Added:
		pair.OldMissing = true
		pair.OldHash = strings.Repeat("0", 64)
		pair.OldMode = "000000"
	case diff.Deleted:
		pair.NewMissing = true
		pair.NewHash = strings.Repeat("0", 64)
		pair.NewMode = "000000"
	}
	return pair
}

func renderDiff(files []diff.FileDiff, mode diffMode, opts diff.Options) []string {
	switch mode {
	case diffModeStat:
		return diff.Stat(files)
	case diffModeNameOnly:
		return diff.NameOnly(files)
	case diffModeNameStatus:
		return diff.NameStatus(files)
	default:
		return diff.Patch(files, opts)
	}
}

func diffUseColor(out io.Writer, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY(out)
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
		strings.HasPrefix(line, "similarity index"),
		strings.HasPrefix(line, "rename from"),
		strings.HasPrefix(line, "rename to"),
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
	case "R":
		return ansiColorYellow + line + ansiColorReset
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

func diffFooter(files int) func(*logViewport) string {
	return func(vp *logViewport) string {
		if vp.total() == 0 {
			return "nipa diff: no changes"
		}
		last := vp.top + len(vp.visibleLines())
		return fmt.Sprintf("nipa diff: %d files, lines %d-%d of %d (q to quit)", files, vp.top+1, last, vp.total())
	}
}
