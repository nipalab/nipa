package cli

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/spf13/cobra"
)

func (c *Cli) setupCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "clone <url> <target>",
		Short:         "Clone a repository",
		Long:          "Clone a repository from a remote source to a target folder on your local machine.",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			target := args[1]
			branch, _ := cmd.Flags().GetString("branch")
			nipaUrl, err := domain.ParseNipaUrl(url)
			if err != nil {
				return err
			}
			ctx := context.Background()
			err = c.connector.Connect(ctx, nipaUrl.Host)
			if err != nil {
				return err
			}
			return c.useCase.Repo().Clone(ctx, nipaUrl.Url, nipaUrl.Host, nipaUrl.Org, nipaUrl.Project, branch, nipaUrl.Path, target, newProgressRenderer(cmd.OutOrStdout()))
		},
	}
	cmd.Flags().StringP("branch", "b", "main", "Specify the branch to clone")
	return cmd
}
