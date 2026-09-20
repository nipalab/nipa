package cli

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupSparseCheckoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sparse-checkout",
		Short: "Limit the working copy to a subset of paths",
		Long: "Limit the working copy to a subset of the repository. Files outside the " +
			"selected directory prefixes are removed from disk but stay on the server; " +
			"re-adding a path only downloads what is missing. Push and update work with " +
			"the sparse set; merge and revert still require a full checkout.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(c.setupSparseListCmd())
	cmd.AddCommand(c.setupSparseSetCmd())
	cmd.AddCommand(c.setupSparseAddCmd())
	cmd.AddCommand(c.setupSparseRemoveCmd())
	cmd.AddCommand(c.setupSparseDisableCmd())
	return cmd
}

func (c *Cli) setupSparseListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List the sparse path prefixes",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, cfg, err := c.sparseRootConfig()
			if err != nil {
				return err
			}
			if len(cfg.Sparse) == 0 {
				cmd.Println("sparse checkout disabled (full checkout)")
				return nil
			}
			for _, path := range cfg.Sparse {
				cmd.Println(path)
			}
			return nil
		},
	}
}

func (c *Cli) setupSparseSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "set <path>...",
		Short:         "Replace the sparse path prefixes and sync the working copy",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _, err := c.sparseRootConfig()
			if err != nil {
				return err
			}
			if err := c.applySparse(root, args); err != nil {
				return err
			}
			cmd.Printf("Sparse checkout set to %d path(s)\n", len(args))
			return nil
		},
	}
}

func (c *Cli) setupSparseAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "add <path>...",
		Short:         "Add sparse path prefixes and sync the working copy",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := c.sparseRootConfig()
			if err != nil {
				return err
			}
			paths := append(append([]string{}, cfg.Sparse...), args...)
			if err := c.applySparse(root, paths); err != nil {
				return err
			}
			cmd.Printf("Added %d path(s)\n", len(args))
			return nil
		},
	}
}

func (c *Cli) setupSparseRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "remove <path>...",
		Short:         "Remove sparse path prefixes and delete their files from disk",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := c.sparseRootConfig()
			if err != nil {
				return err
			}
			drop := make(map[string]struct{}, len(args))
			for _, arg := range args {
				drop[strings.Trim(strings.TrimSpace(arg), "/")] = struct{}{}
			}
			paths := make([]string, 0, len(cfg.Sparse))
			for _, path := range cfg.Sparse {
				if _, ok := drop[path]; ok {
					continue
				}
				paths = append(paths, path)
			}
			if err := c.applySparse(root, paths); err != nil {
				return err
			}
			cmd.Printf("Removed %d path(s)\n", len(args))
			return nil
		},
	}
}

func (c *Cli) setupSparseDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "disable",
		Short:         "Disable sparse checkout and restore the full working copy",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, _, err := c.sparseRootConfig()
			if err != nil {
				return err
			}
			if err := c.applySparse(root, nil); err != nil {
				return err
			}
			cmd.Println("Sparse checkout disabled")
			return nil
		},
	}
}

func (c *Cli) sparseRootConfig() (string, *domain.Config, error) {
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return "", nil, err
	}
	lr := localrepo.NewLocalRepoWithTarget(root)
	cfg, err := lr.LoadConfig()
	if err != nil {
		return "", nil, err
	}
	return root, cfg, nil
}

func (c *Cli) applySparse(root string, paths []string) error {
	update := c.useCase.Update()
	if err := update.SetSparse(root, paths); err != nil {
		return err
	}
	return update.Run(context.Background(), root)
}
