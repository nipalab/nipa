package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

func (c *Cli) setupMrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "mr",
		Short:         "Manage merge requests",
		Long:          "Create, update, list, close and merge merge requests. Commands run against the project of the current working copy; merge requests are fast-forward only, so the target branch must not have moved since the branches diverged.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(c.setupMrCreateCmd())
	cmd.AddCommand(c.setupMrUpdateCmd())
	cmd.AddCommand(c.setupMrListCmd())
	cmd.AddCommand(c.setupMrCloseCmd())
	cmd.AddCommand(c.setupMrMergeCmd())
	return cmd
}

func (c *Cli) setupMrCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "create",
		Short:         "Open a merge request",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			title, _ := cmd.Flags().GetString("title")
			description, _ := cmd.Flags().GetString("description")
			source, _ := cmd.Flags().GetString("source")
			target, _ := cmd.Flags().GetString("target")
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			mr, err := c.useCase.MR().Create(cmd.Context(), root, usecase.CreateMergeRequestOptions{
				Title:       title,
				Description: description,
				Source:      source,
				Target:      target,
			})
			if err != nil {
				return err
			}
			cmd.Printf("Merge request #%d opened: %s -> %s (%s)\n", mr.Number, mr.SourceBranch, mr.TargetBranch, mr.Title)
			return nil
		},
	}
	cmd.Flags().String("title", "", "Merge request title (required)")
	cmd.Flags().StringP("description", "d", "", "Merge request description")
	cmd.Flags().String("source", "", "Source branch (defaults to the current branch)")
	cmd.Flags().String("target", "", "Target branch (defaults to the project default branch)")
	return cmd
}

func (c *Cli) setupMrUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "update <number>",
		Short:         "Update a merge request's title or description",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			description, _ := cmd.Flags().GetString("description")
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			mr, err := c.useCase.MR().Update(cmd.Context(), root, args[0], title, description)
			if err != nil {
				return err
			}
			cmd.Printf("Merge request #%d updated: %s\n", mr.Number, mr.Title)
			return nil
		},
	}
	cmd.Flags().String("title", "", "New title")
	cmd.Flags().StringP("description", "d", "", "New description")
	return cmd
}

func (c *Cli) setupMrListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List merge requests",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, _ := cmd.Flags().GetString("status")
			limit, _ := cmd.Flags().GetInt("limit")
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			requests, err := c.useCase.MR().List(cmd.Context(), root, status, limit)
			if err != nil {
				return err
			}
			if len(requests) == 0 {
				cmd.Println("no merge requests")
				return nil
			}
			cmd.Printf("%-8s  %-7s  %-36s  %s\n", "#", "STATUS", "BRANCHES", "TITLE")
			for _, mr := range requests {
				branches := mr.SourceBranch + " -> " + mr.TargetBranch
				cmd.Printf("%-8d  %-7s  %-36s  %s\n", mr.Number, mr.Status, branches, mr.Title)
			}
			return nil
		},
	}
	cmd.Flags().String("status", "", "Filter by status: open, merged, closed")
	cmd.Flags().Int("limit", 50, "Maximum number of merge requests")
	return cmd
}

func (c *Cli) setupMrCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "close <number>",
		Short:         "Close a merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			mr, err := c.useCase.MR().Close(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			cmd.Printf("Merge request #%d closed.\n", mr.Number)
			return nil
		},
	}
}

func (c *Cli) setupMrMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "merge <number>",
		Short:         "Merge a merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			mr, info, err := c.useCase.MR().Merge(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			cmd.Printf("Merge request #%d merged: %s -> %s", mr.Number, mr.SourceBranch, mr.TargetBranch)
			if info != nil && info.Status != "" {
				cmd.Printf(" (%s)", info.Status)
			}
			cmd.Println()
			return nil
		},
	}
}
