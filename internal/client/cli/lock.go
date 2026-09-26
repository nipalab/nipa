package cli

import (
	"fmt"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/spf13/cobra"
)

func (c *Cli) setupLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "lock <path>",
		Short:         "Lock a binary file or directory so only you can change it",
		Long:          "Tracked binary files can only land when the pusher holds a lock on them. Lock a file before editing it, or a directory prefix to cover a whole editing pass. Adding a brand-new file needs no lock unless a directory or pre-emptive lock guards it. Locks on the default branch are project-global; locks on other branches are scoped to that branch.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			branch, _ := cmd.Flags().GetString("branch")
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			lock, err := c.useCase.Lock().Lock(cmd.Context(), root, args[0], branch)
			if err != nil {
				return err
			}
			cmd.Printf("Locked %s (%s).\n", lock.Path, describeLockScope(lock))
			return nil
		},
	}
	cmd.Flags().String("branch", "", "Branch scope (defaults to the current branch; the default branch is project-global)")
	cmd.AddCommand(c.setupLockListCmd())
	return cmd
}

func (c *Cli) setupLockListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List active binary file locks",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			locks, err := c.useCase.Lock().List(cmd.Context(), root)
			if err != nil {
				return err
			}
			if len(locks) == 0 {
				cmd.Println("no locks")
				return nil
			}
			cmd.Printf("%-40s  %-24s  %-20s  %s\n", "PATH", "SCOPE", "HOLDER", "ACQUIRED")
			for _, lock := range locks {
				via := ""
				if lock.MergeRequestNumber != nil {
					via = fmt.Sprintf("  (MR #%d)", *lock.MergeRequestNumber)
				}
				cmd.Printf("%-40s  %-24s  %-20s  %s%s\n",
					lock.Path, describeLockScope(lock), lockHolder(lock), lock.AcquiredAt.UTC().Format(time.RFC3339), via)
			}
			return nil
		},
	}
}

func (c *Cli) setupUnlockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "unlock <path>",
		Short:         "Release a binary file lock you hold",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			branch, _ := cmd.Flags().GetString("branch")
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			if err := c.useCase.Lock().Unlock(cmd.Context(), root, args[0], branch); err != nil {
				return err
			}
			cmd.Printf("Unlocked %s.\n", args[0])
			return nil
		},
	}
	cmd.Flags().String("branch", "", "Branch scope (defaults to the current branch; the default branch is project-global)")
	return cmd
}

func describeLockScope(lock *domain.FileLock) string {
	if lock.Global {
		return "mainline"
	}
	return fmt.Sprintf("branch %q", lock.Branch)
}

func lockHolder(lock *domain.FileLock) string {
	if lock.HeldByName != "" {
		return lock.HeldByName
	}
	return lock.HeldBy
}
