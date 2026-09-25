package cli

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "branch",
		Short:         "Show the current branch",
		Long:          "Show the current branch of the local working copy, list all branches from the server with -a, create and switch to a new branch with -c, or delete a branch with -d. Searches up to 32 parent directories for a .nipa/config file.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			create, _ := cmd.Flags().GetString("create")
			remove, _ := cmd.Flags().GetString("delete")
			selected := 0
			for _, active := range []bool{all, create != "", remove != ""} {
				if active {
					selected++
				}
			}
			if selected > 1 {
				return fmt.Errorf("--all, --create and --delete are mutually exclusive")
			}
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
			if remove != "" {
				return c.deleteBranch(cmd, cfg, remove)
			}
			cmd.Println(cfg.Branch)
			return nil
		},
	}
	cmd.Flags().BoolP("all", "a", false, "List all branches from the server")
	cmd.Flags().StringP("create", "c", "", "Create a new branch on the server and switch the local working copy to it")
	cmd.Flags().StringP("delete", "d", "", "Delete a branch on the server")
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

func (c *Cli) deleteBranch(cmd *cobra.Command, cfg *domain.Config, name string) error {
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
	if err := c.useCase.Repo().DeleteBranch(ctx, root, nipaUrl.Host, nipaUrl.Org, nipaUrl.Project, name); err != nil {
		return err
	}
	cmd.Printf("Deleted branch %q\n", name)
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
