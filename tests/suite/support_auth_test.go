package suite_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/securestorage"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

type failPrompt struct{}

func (failPrompt) PromptUsernameAndPassword() (string, string, error) {
	return "", "", errors.New("unexpected interactive prompt in client e2e test")
}

func seedCLIToken(host, username, password string) {
	GinkgoHelper()

	store := securestorage.New()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	client := clientgrpc.NewClient(transport, session)

	ctx := context.Background()
	Expect(client.Connect(ctx, host)).To(Succeed())

	result, err := client.LoginWithUsernamePassword(ctx, host, username, password)
	Expect(err).NotTo(HaveOccurred())
	Expect(store.SaveToken(result)).To(Succeed())

	loaded, err := store.LoadToken(host)
	Expect(err).NotTo(HaveOccurred())
	Expect(loaded.AccessToken).NotTo(BeEmpty())
}
