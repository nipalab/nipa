package cli

import (
	"fmt"

	"github.com/spf13/cobra"

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
	if plan == nil {
		return domain.NewUserError("nothing to report")
	}
	if jsonRequested(cmd) {
		return output.WriteJSON(cmd.OutOrStdout(), output.NewPlan(plan))
	}
	out := cmd.OutOrStdout()
	if plan.UpToDate {
		fmt.Fprintln(out, "up to date")
		return nil
	}
	if plan.Kind == "merge" && plan.FastForward {
		fmt.Fprintf(out, "fast-forward %s\n", plan.SourceBranch)
	}
	for _, c := range plan.Changes {
		fmt.Fprintf(out, "%s  %s\n", c.Status, c.Path)
	}
	for _, p := range plan.Conflicts {
		fmt.Fprintf(out, "C  %s\n", p)
	}
	switch plan.Kind {
	case "push":
		fmt.Fprintf(out, "would push %d file(s), %d object(s), %s\n", len(plan.Changes), plan.UploadObjects, humanBytes(plan.UploadBytes))
	case "merge":
		if !plan.FastForward {
			fmt.Fprintf(out, "would merge %d file(s), %d conflict(s)\n", len(plan.Changes), len(plan.Conflicts))
		}
	case "revert":
		fmt.Fprintf(out, "would revert %d file(s), %d conflict(s)\n", len(plan.Changes), len(plan.Conflicts))
	}
	return nil
}
