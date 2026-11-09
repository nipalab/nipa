package cli

import (
	"fmt"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
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
			ttlFlag, _ := cmd.Flags().GetString("ttl")
			var ttl time.Duration
			if ttlFlag != "" {
				parsed, err := time.ParseDuration(ttlFlag)
				if err != nil || parsed <= 0 {
					return domain.NewUserError(fmt.Sprintf("invalid --ttl %q: use a duration such as 30m or 2h", ttlFlag))
				}
				ttl = parsed
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			lock, err := c.useCase.Lock().Lock(cmd.Context(), root, args[0], usecase.LockOptions{Branch: branch, TTL: ttl})
			if err != nil {
				return err
			}
			cmd.Printf("Locked %s (%s%s).\n", lock.Path, describeLockScope(lock), describeLockExpiry(lock, time.Now()))
			if lock.Warning != "" {
				cmd.PrintErrf("warning: %s\n", lock.Warning)
			}
			return nil
		},
	}
	cmd.Flags().String("branch", "", "Branch scope (defaults to the current branch; the default branch is project-global)")
	cmd.Flags().String("ttl", "", "Release the lock automatically after this duration (1m to 168h, e.g. 2h); locking again renews it")
	cmd.AddCommand(c.setupLockListCmd())
	return cmd
}

func (c *Cli) setupLockListCmd() *cobra.Command {
	cmd := &cobra.Command{
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
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), output.NewLocks(locks))
			}
			if len(locks) == 0 {
				cmd.Println("no locks")
				return nil
			}
			now := time.Now()
			cmd.Printf("%-40s  %-24s  %-20s  %-20s  %s\n", "PATH", "SCOPE", "HOLDER", "ACQUIRED", "EXPIRES")
			for _, lock := range locks {
				via := ""
				if lock.MergeRequestNumber != nil {
					via = fmt.Sprintf("  (MR #%d)", *lock.MergeRequestNumber)
				}
				expires := "never"
				if lock.ExpiresAt != nil {
					expires = "in " + formatLockRemaining(lock.ExpiresAt.Sub(now))
				}
				cmd.Printf("%-40s  %-24s  %-20s  %-20s  %s%s\n",
					lock.Path, describeLockScope(lock), lockHolder(lock), lock.AcquiredAt.UTC().Format(time.RFC3339), expires, via)
			}
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
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

func describeLockExpiry(lock *domain.FileLock, now time.Time) string {
	if lock.ExpiresAt == nil {
		return ""
	}
	return ", expires in " + formatLockRemaining(lock.ExpiresAt.Sub(now))
}

func formatLockRemaining(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

func lockHolder(lock *domain.FileLock) string {
	if lock.HeldByName != "" {
		return lock.HeldByName
	}
	return lock.HeldBy
}
