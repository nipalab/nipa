package suite_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	cli        *testEnv
	api        *apiClient
	email      *emailReceiver
	orgSlug    string
	projectSeq int
	messageSeq int
)

func TestClientSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Nipa Client E2E Suite")
}

var _ = BeforeSuite(func() {
	cli = loadEnv()
	if cli == nil {
		Skip("NIPA_TEST_HOST is not set; run the suite via tests/run.sh")
	}

	_, err := os.Stat(cli.binary)
	Expect(err).NotTo(HaveOccurred(), "nipa binary %s not found; build it first", cli.binary)

	seedCLIToken(cli.host, cli.user, cli.password)
	api = newAPIClient(cli.apiURL, cli.user, cli.password)
	email = startEmailReceiver()

	orgSlug = fmt.Sprintf("e2e-%d-%s", time.Now().UnixNano(), randSuffix(3))
	api.createOrg(orgSlug, orgSlug)

	AddReportEntry("server", fmt.Sprintf("edition=%s host=%s org=%s", cli.edition, cli.host, orgSlug))
	GinkgoWriter.Printf("running client e2e against %s server at %s (org %s)\n", cli.edition, cli.host, orgSlug)
})

func newProject(prefix string) string {
	GinkgoHelper()
	projectSeq++
	slug := fmt.Sprintf("%s-%d-%s", prefix, projectSeq, randSuffix(3))
	api.createProject(orgSlug, slug, slug)
	return slug
}

func repoURLFor(org, project string) string {
	return "http://" + cli.host + "/" + org + "/" + project
}

// uniqueMessage keeps commit hashes distinct: the server hashes commits from
// tree, parents and message only, and commits.hash is globally unique, so two
// projects pushing identical first commits with the same message would collide.
func uniqueMessage(message string) string {
	messageSeq++
	return fmt.Sprintf("%s [%d]", message, messageSeq)
}

func randSuffix(n int) string {
	GinkgoHelper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	Expect(err).NotTo(HaveOccurred())
	return hex.EncodeToString(b)
}
