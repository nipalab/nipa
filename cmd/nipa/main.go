package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nipalab/nipa/internal/client/cli"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/securestorage"
	"github.com/nipalab/nipa/internal/client/sshkey"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/obs"
)

func main() {
	stopTracing := startTracing()

	secureStorage := securestorage.New()
	prompter := cli.NewPrompter()

	resolveSigners := func(promptPassphrase func(string) (string, error)) func() ([]sshkey.Signer, error) {
		return func() ([]sshkey.Signer, error) {
			return sshkey.Resolve("", promptPassphrase)
		}
	}

	transport := grpc.NewTransport()
	session := usecase.NewSession(secureStorage, transport, prompter).
		WithSSHLogin(resolveSigners(prompter.PromptPassphrase))
	clientOpts, err := uploadOptions()
	if err != nil {
		handleError(err)
	}
	grpcClient := grpc.NewClient(transport, session, clientOpts...)

	authUsecase := usecase.NewAuth(grpcClient, secureStorage, prompter).
		WithSSHLogin(resolveSigners(prompter.PromptPassphrase)).
		WithCleartextWarning(os.Stderr)
	repoUsecase := usecase.NewRepo(authUsecase, grpcClient, localrepo.NewLocalRepo())
	pushUsecase := usecase.NewPush(authUsecase, grpcClient, localrepo.NewLocalRepo())
	updateUsecase := usecase.NewUpdate(authUsecase, grpcClient, localrepo.NewLocalRepo())
	mergeUsecase := usecase.NewMerge(authUsecase, grpcClient, localrepo.NewLocalRepo(), pushUsecase)
	revertUsecase := usecase.NewRevert(authUsecase, grpcClient, localrepo.NewLocalRepo(), pushUsecase)
	diffUsecase := usecase.NewDiff(authUsecase, grpcClient, localrepo.NewLocalRepo())
	permissionUsecase := usecase.NewPermission(authUsecase, grpcClient)
	mrUsecase := usecase.NewMergeRequest(authUsecase, grpcClient, localrepo.NewLocalRepo())
	lockUsecase := usecase.NewFileLock(authUsecase, grpcClient, localrepo.NewLocalRepo())
	tagUsecase := usecase.NewTag(authUsecase, grpcClient, localrepo.NewLocalRepo())
	versionUsecase := usecase.NewVersion(grpcClient)
	registry := &Registry{
		authUsecase:       authUsecase,
		repoUsecase:       repoUsecase,
		pushUsecase:       pushUsecase,
		updateUsecase:     updateUsecase,
		mergeUsecase:      mergeUsecase,
		revertUsecase:     revertUsecase,
		diffUsecase:       diffUsecase,
		permissionUsecase: permissionUsecase,
		mrUsecase:         mrUsecase,
		lockUsecase:       lockUsecase,
		tagUsecase:        tagUsecase,
		versionUsecase:    versionUsecase,
		blameUsecase:      usecase.NewBlame(authUsecase, grpcClient, localrepo.NewLocalRepo()),
	}

	newClient := func() *grpc.Client {
		transport := grpc.NewTransport()
		session := usecase.NewSession(secureStorage, transport).
			WithSSHLogin(resolveSigners(nil))
		opts, err := uploadOptions()
		if err != nil {
			opts = clientOpts
		}
		return grpc.NewClient(transport, session, opts...)
	}

	cliClient := cli.NewCli(registry, grpcClient)
	if err := cliClient.Run(newServeCommand(authUsecase, newClient, secureStorage, resolveSigners(nil)), newMCPCommand(newClient, secureStorage)); err != nil {
		stopTracing()
		if errors.Is(err, cli.ErrExitCode) {
			os.Exit(1)
		}
		if errors.Is(err, cli.ErrBudgetViolations) {
			os.Exit(cli.ExitCode(err))
		}
		handleError(err)
	}
	stopTracing()
}

// startTracing boots the client tracer from the environment and returns a
// flush function the process must call before exiting. Failures disable
// tracing without failing the command.
func startTracing() func() {
	cfg, err := config.ResolveTracing()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: tracing disabled:", err)
		return func() {}
	}
	if !cfg.Enabled {
		return func() {}
	}
	tracing, err := obs.SetupTracing(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: tracing disabled:", err)
		return func() {}
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracing.Shutdown(ctx)
	}
}

func handleError(err error) {
	if cli.JSONRequested(os.Args[1:]) {
		_ = output.WriteError(os.Stderr, err)
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(cli.ExitCode(err))
}

func uploadOptions() ([]grpc.ClientOption, error) {
	workers, pinned, err := config.ResolveUploadWorkers()
	if err != nil || !pinned {
		return nil, err
	}
	return []grpc.ClientOption{grpc.WithUploadWorkers(workers)}, nil
}
