package cli

import (
	"context"

	clientconfig "github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/difftool"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	Repo() *usecase.Repo
	Push() *usecase.Push
	Update() *usecase.Update
	Merge() *usecase.Merge
	Diff() *usecase.Diff
}

type connector interface {
	Connect(ctx context.Context, host string) error
}

type Cli struct {
	useCase        usecaseContainer
	connector      connector
	externalRunner difftool.Runner
	userConfig     func() (clientconfig.Config, error)
}

func NewCli(useCase usecaseContainer, connector connector) *Cli {
	return &Cli{
		useCase:        useCase,
		connector:      connector,
		externalRunner: difftool.ExecRunner{},
		userConfig:     clientconfig.Load,
	}
}

func (c *Cli) Run() error {
	var rootCmd = &cobra.Command{
		Use:   "nipa",
		Short: "nipa is centralized version control system for your project",
	}
	rootCmd.AddCommand(c.setupCloneCmd())
	rootCmd.AddCommand(c.setupBranchCmd())
	rootCmd.AddCommand(c.setupAddCmd())
	rootCmd.AddCommand(c.setupRemoveCmd())
	rootCmd.AddCommand(c.setupStatusCmd())
	rootCmd.AddCommand(c.setupPushCmd())
	rootCmd.AddCommand(c.setupUpdateCmd())
	rootCmd.AddCommand(c.setupSwitchCmd())
	rootCmd.AddCommand(c.setupMergeCmd())
	rootCmd.AddCommand(c.setupLogCmd())
	rootCmd.AddCommand(c.setupDiffCmd())
	return rootCmd.Execute()
}
