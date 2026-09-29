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
	"github.com/nipalab/nipa/internal/client/securestorage"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// nonInteractiveInput replaces the terminal prompter in MCP mode: a prompt
// would write to stdout and corrupt the JSON-RPC stream.
type nonInteractiveInput struct{}

func (nonInteractiveInput) PromptUsernameAndPassword() (string, string, error) {
	return "", "", domain.NewUserError("interactive login is not available in mcp mode").
		WithHint("authenticate from a terminal first", "nipa clone <url> <target>")
}

func newMCPCommand(newClient func() *clientgrpc.Client, storage *securestorage.Storage) *cobra.Command {
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
				RepoDir:    repoDir,
				AllowWrite: allowWrite,
				Version:    daemon.Version,
				NewUseCases: func() mcpserver.UseCases {
					client := newClient()
					auth := usecase.NewAuth(client, storage, nonInteractiveInput{})
					push := usecase.NewPush(auth, client, localrepo.NewLocalRepo())
					return mcpserver.UseCases{
						Repo:      usecase.NewRepo(auth, client, localrepo.NewLocalRepo()),
						Push:      push,
						Diff:      usecase.NewDiff(auth, client, localrepo.NewLocalRepo()),
						MR:        usecase.NewMergeRequest(auth, client, localrepo.NewLocalRepo()),
						Lock:      usecase.NewFileLock(auth, client, localrepo.NewLocalRepo()),
						Connector: client,
					}
				},
			})
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "Path inside the nipa working copy to operate on (defaults to the working directory)")
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "Register mutating tools (add, push, lock, branch and merge-request creation)")
	return cmd
}
