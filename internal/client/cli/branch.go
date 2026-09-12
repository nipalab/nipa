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
		Long:          "Show the current branch of the local working copy, list all branches from the server with -a, or create and switch to a new branch with -c. Searches up to 32 parent directories for a .nipa/config file.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			create, _ := cmd.Flags().GetString("create")
			cfg, err := c.loadConfig()
			if err != nil {
				return err
			}
			if all {
				return c.listAllBranches(cmd, cfg)
			}
			if create != "" {
				return c.createBranch(cmd, cfg, create)
			}
			cmd.Println(cfg.Branch)
			return nil
		},
	}
	cmd.Flags().BoolP("all", "a", false, "List all branches from the server")
	cmd.Flags().StringP("create", "c", "", "Create a new branch on the server and switch the local working copy to it")
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

func (c *Cli) createBranch(cmd *cobra.Command, cfg *domain.Config, name string) error {
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := c.connector.Connect(ctx, nipaUrl.Host); err != nil {
		return err
	}
	created, err := c.useCase.Repo().CreateBranch(ctx, root, nipaUrl.Host, nipaUrl.Org, nipaUrl.Project, name)
	if err != nil {
		return err
	}
	cmd.Printf("Created and switched to branch %q\n", created.Name)
	return nil
}

func (c *Cli) listAllBranches(cmd *cobra.Command, cfg *domain.Config) error {
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
		if line == cfg.Branch {
			line += " *"
		}
		cmd.Println(line)
	}
	return nil
}
