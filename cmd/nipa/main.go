package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nipalab/nipa/internal/client/cli"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/securestorage"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func main() {
	secureStorage := securestorage.New()
	prompter := cli.NewPrompter()

	transport := grpc.NewTransport()
	session := usecase.NewSession(secureStorage, transport, prompter)
	var clientOpts []grpc.ClientOption
	if workers, pinned, err := config.ResolveUploadWorkers(); err != nil {
		handleError(err)
	} else if pinned {
		clientOpts = append(clientOpts, grpc.WithUploadWorkers(workers))
	}
	grpcClient := grpc.NewClient(transport, session, clientOpts...)

	authUsecase := usecase.NewAuth(grpcClient, secureStorage, prompter)
	repoUsecase := usecase.NewRepo(authUsecase, grpcClient, localrepo.NewLocalRepo())
	pushUsecase := usecase.NewPush(authUsecase, grpcClient, localrepo.NewLocalRepo())
	updateUsecase := usecase.NewUpdate(authUsecase, grpcClient, localrepo.NewLocalRepo())
	mergeUsecase := usecase.NewMerge(authUsecase, grpcClient, localrepo.NewLocalRepo(), pushUsecase)
	revertUsecase := usecase.NewRevert(authUsecase, grpcClient, localrepo.NewLocalRepo(), pushUsecase)
	diffUsecase := usecase.NewDiff(authUsecase, grpcClient, localrepo.NewLocalRepo())
	permissionUsecase := usecase.NewPermission(authUsecase, grpcClient)
	mrUsecase := usecase.NewMergeRequest(authUsecase, grpcClient, localrepo.NewLocalRepo())
	lockUsecase := usecase.NewFileLock(authUsecase, grpcClient, localrepo.NewLocalRepo())
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
	}

	cliClient := cli.NewCli(registry, grpcClient)
	if err := cliClient.Run(); err != nil {
		if errors.Is(err, cli.ErrExitCode) {
			os.Exit(1)
		}
		handleError(err)
	}
}

func handleError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
