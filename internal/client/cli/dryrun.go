package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
)

func addDryRunFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, "Report what would happen without changing the working copy, the local state or the server")
	addJSONFlag(cmd)
}

func dryRunRequested(cmd *cobra.Command) (bool, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if !dryRun && cmd.Flags().Lookup("json") != nil && cmd.Flags().Changed("json") {
		return false, domain.NewUserError("--json requires --dry-run")
	}
	return dryRun, nil
}

func writePlan(cmd *cobra.Command, plan *domain.Plan) error {
	return writePlanWithBudget(cmd, plan, nil, "")
}

func writePlanWithBudget(cmd *cobra.Command, plan *domain.Plan, violations []assetbudget.Violation, generated string) error {
	if plan == nil {
		return domain.NewUserError("nothing to report")
	}
	if jsonRequested(cmd) {
		out := output.NewPlan(plan)
		out.AssetViolations = violations
		out.GeneratedMessage = generated
		return output.WriteJSON(cmd.OutOrStdout(), out)
	}
	out := cmd.OutOrStdout()
	if plan.UpToDate {
		_, _ = fmt.Fprintln(out, "up to date")
		return nil
	}
	if plan.Kind == "merge" && plan.FastForward {
		_, _ = fmt.Fprintf(out, "fast-forward %s\n", plan.SourceBranch)
	}
	for _, c := range plan.Changes {
		_, _ = fmt.Fprintf(out, "%s  %s\n", c.Status, c.Path)
	}
	for _, p := range plan.Conflicts {
		_, _ = fmt.Fprintf(out, "C  %s\n", p)
	}
	switch plan.Kind {
	case "push":
		_, _ = fmt.Fprintf(out, "would push %d file(s), %d object(s), %s\n", len(plan.Changes), plan.UploadObjects, humanBytes(plan.UploadBytes))
	case "merge":
		if !plan.FastForward {
			_, _ = fmt.Fprintf(out, "would merge %d file(s), %d conflict(s)\n", len(plan.Changes), len(plan.Conflicts))
		}
	case "revert":
		_, _ = fmt.Fprintf(out, "would revert %d file(s), %d conflict(s)\n", len(plan.Changes), len(plan.Conflicts))
	}
	if len(violations) > 0 {
		_, _ = fmt.Fprintf(out, "warning: %d asset budget violation(s) (see nipa assets check --staged):\n", len(violations))
		writeViolations(out, "  ", violations)
	}
	if generated != "" {
		_, _ = fmt.Fprintf(out, "generated commit message:\n\n%s\n", generated)
	}
	return nil
}
