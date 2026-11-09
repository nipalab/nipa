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
	var repoDir, allowList string
	var allowWrite, noAgentTrailer bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server over stdio",
		Long: "Serve the working copy to AI agents as MCP tools over stdin/stdout.\n\n" +
			"Always available: read tools (status, diff, log, branches, tags, locks,\n" +
			"conflicts, merge requests with diff/threads/commits/timeline), asset tools\n" +
			"(structured asset diffs, change summaries, .nipaassets.yml checks, Unity and\n" +
			"Godot sidecar warnings) and the prompts nipa_commit_message,\n" +
			"nipa_mr_description and nipa_review_mr.\n\n" +
			"Write tools are enabled per capability with --allow (comma list):\n" +
			"  stage   add, restore\n" +
			"  push    push\n" +
			"  sync    update, switch\n" +
			"  branch  branch and tag creation\n" +
			"  lock    lock, unlock\n" +
			"  mr      merge request create, update, comment, reply, resolve, review,\n" +
			"          request-review, check\n" +
			"  merge   merge, conflict resolution, Godot scene merge, merge request merge\n" +
			"--allow-write grants all of them. Text written through MCP gets a\n" +
			"\"Nipa-Agent: <client>/<version>\" trailer unless --no-agent-trailer is set.\n\n" +
			"Authentication never prompts: log in from a terminal first (nipa clone).\n" +
			"Example: claude mcp add nipa -- nipa mcp --allow mr,lock",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			allow, err := mcpserver.ParseAllow(allowList)
			if err != nil {
				return err
			}
			if allowWrite {
				allow = mcpserver.AllCaps()
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return mcpserver.Serve(ctx, mcpserver.Options{
				RepoDir:        repoDir,
				Allow:          allow,
				NoAgentTrailer: noAgentTrailer,
				Version:        daemon.Version,
				NewUseCases:    func() mcpserver.UseCases { return newMCPUseCases(newClient, storage) },
			})
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "Path inside the nipa working copy to operate on (defaults to the working directory)")
	cmd.Flags().StringVar(&allowList, "allow", "", "Comma-separated write capabilities to enable: stage, push, sync, branch, lock, mr, merge")
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "Enable every write capability (same as --allow stage,push,sync,branch,lock,mr,merge)")
	cmd.Flags().BoolVar(&noAgentTrailer, "no-agent-trailer", false, "Do not append the Nipa-Agent trailer to text written through MCP")
	return cmd
}

// newMCPUseCases builds one throwaway usecase graph per tool call and returns
// it with a Close that releases every connection and SQLite handle the call
// opened, so a long-running server does not leak descriptors.
func newMCPUseCases(newClient func() *clientgrpc.Client, storage tokenStorage) mcpserver.UseCases {
	client := newClient()
	auth := usecase.NewAuth(client, storage, nonInteractiveInput{})
	repos := make([]*localrepo.LocalRepo, 0, 8)
	newLocalRepo := func() *localrepo.LocalRepo {
		lr := localrepo.NewLocalRepo()
		repos = append(repos, lr)
		return lr
	}
	push := usecase.NewPush(auth, client, newLocalRepo())
	uc := mcpserver.UseCases{
		Repo:      usecase.NewRepo(auth, client, newLocalRepo()),
		Push:      push,
		Diff:      usecase.NewDiff(auth, client, newLocalRepo()).WithPrune(),
		MR:        usecase.NewMergeRequest(auth, client, newLocalRepo()),
		Lock:      usecase.NewFileLock(auth, client, newLocalRepo()),
		Tag:       usecase.NewTag(auth, client, newLocalRepo()),
		Update:    usecase.NewUpdate(auth, client, newLocalRepo()),
		Merge:     usecase.NewMerge(auth, client, newLocalRepo(), push),
		Connector: client,
		Auth:      auth,
	}
	uc.Close = func() {
		for _, lr := range repos {
			_ = lr.Close()
		}
		_ = client.Close()
	}
	return uc
}
