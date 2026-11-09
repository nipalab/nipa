package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
)

// ErrBudgetViolations is returned once the violations were printed, so the
// process exits 2 without another message.
var ErrBudgetViolations = &ExitError{Code: 2, Err: errors.New("asset budget violations found")}

func (c *Cli) setupAssetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assets",
		Short: "Inspect assets against the team rules",
	}
	cmd.AddCommand(c.setupAssetsCheckCmd())
	return cmd
}

func (c *Cli) setupAssetsCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "check [<rev1> [<rev2>]] [--] [<path>...]",
		Short:         "Check changed files against the .nipaassets.yml budgets",
		Long:          "Check the new side of every changed file against the asset budgets in the working copy's root .nipaassets.yml: texture size and power of two, triangle and vertex counts, audio length and peak, file size and naming. Revisions select the compared sides like nipa diff (none: working tree against the last synced snapshot, offline; <a>..<b> and <a>...<b> ranges are accepted); the budget file is always read from the working copy. Exits 2 when a budget is exceeded.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runAssetsCheck(cmd, args)
		},
	}
	cmd.Flags().Bool("staged", false, "Only check staged changes")
	cmd.Flags().Bool("no-cache", false, "Read every working file instead of trusting the stat cache")
	addJSONFlag(cmd)
	return cmd
}

func (c *Cli) runAssetsCheck(cmd *cobra.Command, args []string) error {
	revs, paths, err := splitDiffArgs(cmd, args)
	if err != nil {
		return err
	}
	revs, mergeBase, err := expandRevisionRange(revs)
	if err != nil {
		return err
	}
	opts, err := diffCompareOptions(cmd, revs, paths, mergeBase)
	if err != nil {
		return err
	}
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return err
	}
	report, err := assetscan.Check(cmd.Context(), c.useCase.Diff(), root, assetscan.Request{
		Revisions: revs, Paths: opts.Paths, Staged: opts.Staged, MergeBase: opts.MergeBase, NoCache: opts.NoCache,
	})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if jsonRequested(cmd) {
		if err := output.WriteJSON(out, report); err != nil {
			return err
		}
	} else {
		writeAssetReport(out, report)
	}
	if len(report.Violations) > 0 {
		return ErrBudgetViolations
	}
	return nil
}

func writeAssetReport(out io.Writer, report *assetscan.Report) {
	if !report.Config {
		_, _ = fmt.Fprintf(out, "no %s\n", assetbudget.FileName)
		return
	}
	writeViolations(out, "", report.Violations)
	for _, s := range report.Skipped {
		_, _ = fmt.Fprintf(out, "skipped %s: %s\n", s.Path, s.Reason)
	}
	_, _ = fmt.Fprintf(out, "%d violation(s) in %d checked file(s)\n", len(report.Violations), report.Checked)
}

func writeViolations(out io.Writer, indent string, violations []assetbudget.Violation) {
	for _, v := range violations {
		_, _ = fmt.Fprintf(out, "%s%s: %s: %s (rule %s)\n", indent, v.Path, v.Limit, v.Message, v.Rule)
	}
}

// pushBudget checks the staged changes of a push plan against the budget,
// offline: only working files are read.
func (c *Cli) pushBudget(cmd *cobra.Command, root string, plan *domain.Plan) ([]assetbudget.Violation, error) {
	d := c.useCase.Diff()
	if plan == nil || d == nil {
		return nil, nil
	}
	cfg, err := assetbudget.Load(root)
	if err != nil || cfg == nil {
		return nil, err
	}
	report, err := assetscan.CheckChanges(cmd.Context(), assetscan.BlobLoader(d, root), cfg, planChanges(plan))
	if err != nil {
		return nil, err
	}
	return report.Violations, nil
}

func planChanges(plan *domain.Plan) *assetscan.Changes {
	changes := &assetscan.Changes{Old: assetscan.SideSnapshot, New: assetscan.SideWorking}
	for _, ch := range plan.Changes {
		changes.Files = append(changes.Files, assetscan.Change{
			Item:    assetscan.ChangeItem(ch.Path, "", ch.Status, assetscan.SideSnapshot, assetscan.SideWorking),
			NewSize: ch.SizeBytes,
		})
	}
	return changes
}

// pushImpact checks the staged changes of a push plan for broken asset
// references, offline. It only warns: a failure is printed and skipped.
func (c *Cli) pushImpact(cmd *cobra.Command, root string, plan *domain.Plan) *assetrefs.Impact {
	d := c.useCase.Diff()
	if plan == nil || d == nil {
		return nil
	}
	impact, err := assetscan.Impact(cmd.Context(), d, root, planChanges(plan), assetrefs.Options{})
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: impact check skipped: %v\n", err)
		return nil
	}
	if impact.Empty() {
		return nil
	}
	return impact
}

func writeImpact(out io.Writer, impact *assetrefs.Impact) {
	_, _ = fmt.Fprintf(out, "warning: %d broken reference(s), %d newly unused asset(s) (unstaged working changes included):\n", len(impact.Broken), len(impact.NewlyUnused))
	for _, b := range impact.Broken {
		detail := b.Reason
		if b.Target != "" {
			detail += ": " + b.Target
		}
		_, _ = fmt.Fprintf(out, "  %s -> %s (%s)\n", b.From, b.Raw, detail)
	}
	for _, p := range impact.NewlyUnused {
		_, _ = fmt.Fprintf(out, "  unused: %s\n", p)
	}
	if impact.Truncated {
		_, _ = fmt.Fprintf(out, "  (scan truncated after %d files)\n", impact.Scanned)
	}
}
