package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	mcpserver "github.com/nipalab/nipa/internal/client/mcp"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// nonInteractiveInput replaces the terminal prompter in MCP mode: a prompt
// would write to stdout and corrupt the JSON-RPC stream.
type nonInteractiveInput struct{}

func (nonInteractiveInput) PromptUsernameAndPassword() (string, string, error) {
	return "", "", domain.NewUserError("interactive login is not available in mcp mode").
		WithHint("authenticate from a terminal first", "nipa clone <url> <target>")
}

type tokenStorage interface {
	SaveToken(*domain.LoginResult) error
	LoadToken(host string) (*domain.LoginResult, error)
}

func newMCPCommand(newClient func() *clientgrpc.Client, storage tokenStorage) *cobra.Command {
	var repoDir string
	var allowWrite bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server over stdio",
		Long: "Serve Nipa as MCP tools for AI agents over stdin/stdout. Read-only tools\n" +
			"(status, diff, log, branch and merge-request listing, locks) are always\n" +
			"available; mutating tools (add, push, branch/lock/merge-request creation)\n" +
			"require --allow-write. Tokens come from the same store as the CLI, so log\n" +
			"in with a regular clone first.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return mcpserver.Serve(ctx, mcpserver.Options{
				RepoDir:     repoDir,
				AllowWrite:  allowWrite,
				Version:     daemon.Version,
				NewUseCases: func() mcpserver.UseCases { return newMCPUseCases(newClient, storage) },
			})
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "Path inside the nipa working copy to operate on (defaults to the working directory)")
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "Register mutating tools (add, push, lock, branch and merge-request creation)")
	return cmd
}

// newMCPUseCases builds one throwaway usecase graph per tool call and returns
// it with a Close that releases every connection and SQLite handle the call
// opened, so a long-running server does not leak descriptors.
func newMCPUseCases(newClient func() *clientgrpc.Client, storage tokenStorage) mcpserver.UseCases {
	client := newClient()
	auth := usecase.NewAuth(client, storage, nonInteractiveInput{})
	repos := make([]*localrepo.LocalRepo, 0, 5)
	newLocalRepo := func() *localrepo.LocalRepo {
		lr := localrepo.NewLocalRepo()
		repos = append(repos, lr)
		return lr
	}
	push := usecase.NewPush(auth, client, newLocalRepo())
	uc := mcpserver.UseCases{
		Repo:      usecase.NewRepo(auth, client, newLocalRepo()),
		Push:      push,
		Diff:      usecase.NewDiff(auth, client, newLocalRepo()),
		MR:        usecase.NewMergeRequest(auth, client, newLocalRepo()),
		Lock:      usecase.NewFileLock(auth, client, newLocalRepo()),
		Connector: client,
	}
	uc.Close = func() {
		for _, lr := range repos {
			_ = lr.Close()
		}
		_ = client.Close()
	}
	return uc
}
