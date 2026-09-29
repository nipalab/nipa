package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/output"
)

func addJSONFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("json", false, "Print machine-readable JSON instead of text")
}

func jsonRequested(cmd *cobra.Command) bool {
	if on, _ := cmd.Flags().GetBool("json"); on {
		return true
	}
	return output.FromEnv()
}

// JSONRequested reports whether the invocation asked for JSON output, so the
// top-level error handler can emit the error envelope in the same format.
func JSONRequested(args []string) bool {
	if output.FromEnv() {
		return true
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" {
			return true
		}
		if value, ok := strings.CutPrefix(arg, "--json="); ok {
			return !strings.EqualFold(value, "false")
		}
	}
	return false
}
