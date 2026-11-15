package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/ignore"
	"github.com/nipalab/nipa/internal/client/localrepo"
)

func (c *Cli) setupIgnoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ignore",
		Short: "Manage the .nipaignore rules",
	}
	cmd.AddCommand(&cobra.Command{
		Use:           "import",
		Short:         "Copy the root .gitignore rules into .nipaignore",
		Long:          "Append the rules of the repository root .gitignore that .nipaignore does not already hold, creating .nipaignore when missing. Nipa never reads .gitignore itself. Rules using gitignore syntax Nipa does not support (character classes such as [Bb], backslash escapes) are copied but listed, since they will not match. Stage .nipaignore afterwards to share the rules.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			res, err := ignore.ImportGitignore(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "added %d rules to %s\n", res.Added, ignore.FileName)
			for _, line := range res.Unsupported {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %q uses gitignore syntax nipa does not support and will not match\n", line)
			}
			return nil
		},
	})
	return cmd
}
