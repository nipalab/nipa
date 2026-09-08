package cli

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "branch",
		Short:         "Show the current branch",
		Long:          "Show the current branch of the local working copy, or list all branches from the server with -a. Searches up to 32 parent directories for a .nipa/config file.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				return c.listAllBranches(cmd)
			}
			cfg, err := c.loadConfig()
			if err != nil {
				return err
			}
			cmd.Println(cfg.Branch)
			return nil
		},
	}
	cmd.Flags().BoolP("all", "a", false, "List all branches from the server")
	return cmd
}

func (c *Cli) loadConfig() (*domain.Config, error) {
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return nil, err
	}
	lr := localrepo.NewLocalRepoWithTarget(root)
	cfg, err := lr.LoadConfig()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Cli) listAllBranches(cmd *cobra.Command) error {
	cfg, err := c.loadConfig()
	if err != nil {
		return err
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := c.connector.Connect(ctx, nipaUrl.Host); err != nil {
		return err
	}
	branches, err := c.useCase.Repo().ListBranches(ctx, nipaUrl.Host, nipaUrl.Org, nipaUrl.Project)
	if err != nil {
		return err
	}
	for _, b := range branches {
		line := b.Name
		if b.IsDefault {
			line += " *"
		}
		cmd.Println(line)
	}
	return nil
}
