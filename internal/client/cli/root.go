package cli

import (
	"context"

	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	Repo() *usecase.Repo
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
	return rootCmd.Execute()
}
