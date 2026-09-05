package main

import (
	"fmt"
	"os"

	"github.com/nipalab/nipa/internal/client/cli"
	"github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/securestorage"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func main() {
	secureStorage := securestorage.New()
	prompter := cli.NewPrompter()

	authUsecase := usecase.NewAuth(nil, secureStorage, prompter)
	grpcClient := grpc.NewClient(authUsecase)
	authUsecase.SetLoginExecutor(grpcClient)

	repoUsecase := usecase.NewRepo(authUsecase, grpcClient)
	registry := &Registry{
		authUsecase: authUsecase,
		repoUsecase: repoUsecase,
	}

	cliClient := cli.NewCli(registry)
	if err := cliClient.Run(); err != nil {
		handleError(err)
	}
}

func handleError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
