package cli

import (
	"context"

	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	Repo() *usecase.Repo
	Push() *usecase.Push
	Update() *usecase.Update
}

type connector interface {
	Connect(ctx context.Context, host string) error
}

type Cli struct {
	useCase   usecaseContainer
	connector connector
}

func NewCli(useCase usecaseContainer, connector connector) *Cli {
	return &Cli{
		useCase:   useCase,
		connector: connector,
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
	return rootCmd.Execute()
}
