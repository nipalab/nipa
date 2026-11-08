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
		Long: "Serve Nipa as MCP tools for AI agents over stdin/stdout. Read-only tools\n" +
			"(status, diff, log, branches, tags, locks, conflicts, merge requests with\n" +
			"their diff, threads, commits and timeline, plus asset-aware tools: structured\n" +
			"asset diffs with image thumbnails, per-file change summaries for the working\n" +
			"copy, revisions or a merge request, and Unity/Godot sidecar warnings) are\n" +
			"always available. Mutating\n" +
			"tools are enabled per capability with --allow (comma list):\n" +
			"  stage   add, restore\n" +
			"  push    push\n" +
			"  sync    update, switch (branch or tag)\n" +
			"  branch  branch and tag creation\n" +
			"  lock    lock, unlock\n" +
			"  mr      merge-request create, update/draft/ready, comment, reply, resolve,\n" +
			"          review, request-review, check report\n" +
			"  merge   local merge (and abort), conflict resolution, structural Godot\n" +
			"          .tscn/.tres merge, merge-request merge\n" +
			"--allow-write grants every capability. Text written through MCP (push\n" +
			"messages, merge-request descriptions, comments, replies, reviews) gets a\n" +
			"\"Nipa-Agent: <client>/<version>\" trailer unless --no-agent-trailer is set.\n" +
			"Diffs are capped by max_bytes (default 256 KiB, at most 2 MiB), logs by 500\n" +
			"commits. Tokens come from the same store as the CLI, so log in with a regular\n" +
			"clone first.",
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
		Diff:      usecase.NewDiff(auth, client, newLocalRepo()),
		MR:        usecase.NewMergeRequest(auth, client, newLocalRepo()),
		Lock:      usecase.NewFileLock(auth, client, newLocalRepo()),
		Tag:       usecase.NewTag(auth, client, newLocalRepo()),
		Update:    usecase.NewUpdate(auth, client, newLocalRepo()),
		Merge:     usecase.NewMerge(auth, client, newLocalRepo(), push),
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
