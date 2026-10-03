package suite_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// identity is one CLI user: a server-side account plus the token file its
// invocations authenticate with.
type identity struct {
	name      string
	email     string
	password  string
	userID    string
	tokenFile string
}

func newIdentity(prefix string) *identity {
	GinkgoHelper()

	suffix := fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), randSuffix(3))
	id := &identity{
		name:      prefix,
		email:     suffix + "@example.com",
		password:  "identity-password-123",
		tokenFile: filepath.Join(GinkgoT().TempDir(), "tokens.json"),
	}
	id.userID = api.createUser(id.name, id.email, id.password)
	seedTokenFile(id.tokenFile, cli.host, id.email, id.password)

	DeferCleanup(func() { _ = os.Remove(id.tokenFile) })
	return id
}

func runNipaAs(id *identity, cwd string, args ...string) commandResult {
	GinkgoHelper()
	return runNipaEnv(cwd, []string{"NIPA_TOKEN_FILE=" + id.tokenFile}, args...)
}

func cloneRepoAs(id *identity, parent, url, target string, extraArgs ...string) string {
	GinkgoHelper()
	args := append([]string{"clone", url, target}, extraArgs...)
	res := runNipaAs(id, parent, args...)
	Expect(res.ExitCode).To(Equal(0), "clone failed: %s", res.Output())
	dir := filepath.Join(parent, target)
	Expect(fileExists(dir, ".nipa/config")).To(BeTrue(), ".nipa/config missing after clone")
	return dir
}

// promote grants the identity global admin rights so lock and merge-request
// specs can exercise non-owner holders without setting up PBAC rules first.
// The token is re-minted because the admin claim is embedded at login time.
func (id *identity) promote() *identity {
	GinkgoHelper()
	api.setAdmin(id.userID)
	seedTokenFile(id.tokenFile, cli.host, id.email, id.password)
	return id
}
