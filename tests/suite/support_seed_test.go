package suite_test

import (
	"context"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/securestorage"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

// seedRun uses the client usecases in-process to land a change on the server.
// It is test setup, independent of the compiled CLI under test. It returns the
// unique commit message it pushed.
func seedRun(org, project, message string, mutate func(dir string) []string) string {
	GinkgoHelper()

	ctx := context.Background()
	store := securestorage.New()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	client := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(client, store, failPrompt{})
	Expect(client.Connect(ctx, cli.host)).To(Succeed())

	dir := GinkgoT().TempDir()
	repo := clientusecase.NewRepo(auth, client, localrepo.NewLocalRepo())
	url := repoURLFor(org, project)
	Expect(repo.Clone(ctx, url, cli.host, org, project, "", nil, dir)).To(Succeed())

	lr := localrepo.NewLocalRepo()
	Expect(lr.Init(dir)).To(Succeed())
	defer func() { _ = lr.Close() }()

	for _, path := range mutate(dir) {
		Expect(lr.StageAdd(path)).To(Succeed())
	}

	unique := uniqueMessage(message)
	pusher := clientusecase.NewPush(auth, client, localrepo.NewLocalRepo())
	Expect(pusher.Run(ctx, dir, unique)).To(Succeed())
	return unique
}

func seedRepo(org, project string, files map[string][]byte, message string) string {
	GinkgoHelper()
	return seedRun(org, project, message, func(dir string) []string {
		paths := make([]string, 0, len(files))
		for path, data := range files {
			writeBytes(dir, path, data)
			paths = append(paths, path)
		}
		sort.Strings(paths)
		return paths
	})
}

func seedRepoDelete(org, project string, paths []string, message string) string {
	GinkgoHelper()
	return seedRun(org, project, message, func(dir string) []string {
		for _, path := range paths {
			removePath(dir, path)
		}
		return paths
	})
}
