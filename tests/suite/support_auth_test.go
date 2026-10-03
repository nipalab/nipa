package suite_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/securestorage"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

type failPrompt struct{}

func (failPrompt) PromptUsernameAndPassword() (string, string, error) {
	return "", "", errors.New("unexpected interactive prompt in client e2e test")
}

type noopStore struct{}

func (noopStore) SaveToken(*clientdomain.LoginResult) error { return nil }
func (noopStore) LoadToken(string) (*clientdomain.LoginResult, error) {
	return nil, errors.New("no token stored")
}

func loginResult(host, username, password string) *clientdomain.LoginResult {
	GinkgoHelper()

	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(noopStore{}, transport, failPrompt{})
	client := clientgrpc.NewClient(transport, session)

	ctx := context.Background()
	Expect(client.Connect(ctx, host)).To(Succeed())
	result, err := client.LoginWithUsernamePassword(ctx, host, username, password)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.AccessToken).NotTo(BeEmpty())
	return result
}

// seedTokenFile logs in and writes the token into a NIPA_TOKEN_FILE-format
// file, so every CLI invocation using that file is authenticated.
func seedTokenFile(tokenFile, host, username, password string) {
	GinkgoHelper()

	result := loginResult(host, username, password)

	tokens := map[string]securestorage.AccessToken{}
	if data, err := os.ReadFile(tokenFile); err == nil && len(data) > 0 {
		Expect(json.Unmarshal(data, &tokens)).To(Succeed())
	}
	tokens[host] = securestorage.AccessToken{
		Host:         host,
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
	}
	data, err := json.Marshal(tokens)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.MkdirAll(filepath.Dir(tokenFile), 0o700)).To(Succeed())
	Expect(os.WriteFile(tokenFile, data, 0o600)).To(Succeed())
}

func seedCLIToken(host, username, password string) {
	GinkgoHelper()
	seedTokenFile(cli.tokenFile, host, username, password)
}
