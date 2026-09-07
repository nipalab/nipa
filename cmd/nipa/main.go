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

	transport := grpc.NewTransport()
	session := usecase.NewSession(secureStorage, transport)
	grpcClient := grpc.NewClient(transport, session)

	authUsecase := usecase.NewAuth(grpcClient, secureStorage, prompter)
	repoUsecase := usecase.NewRepo(authUsecase, grpcClient)
	registry := &Registry{
		authUsecase: authUsecase,
		repoUsecase: repoUsecase,
	}

	cliClient := cli.NewCli(registry, grpcClient)
	if err := cliClient.Run(); err != nil {
		handleError(err)
	}
}

func handleError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
