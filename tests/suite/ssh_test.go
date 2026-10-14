package suite_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa ssh key login", func() {
	It("logs in with a registered key without a username", func() {
		dir := workspace()
		keyPath, publicKey := newSSHKeyPair(dir)

		key := api.addSSHKey("e2e key", publicKey)
		DeferCleanup(func() { api.removeSSHKey(key.ID) })
		Expect(key.KeyType).To(Equal("ssh-ed25519"))
		Expect(key.Fingerprint).To(HavePrefix("SHA256:"))

		Expect(api.listSSHKeys()).To(ContainElement(HaveField("ID", key.ID)))

		tokenFile := filepath.Join(dir, "tokens.json")
		res := runNipaEnv(dir, []string{"NIPA_TOKEN_FILE=" + tokenFile},
			"login", "--host", cli.host, "--ssh-key", keyPath)
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(res.Output()).To(ContainSubstring("using SSH key"))

		var tokens map[string]struct {
			AccessToken string `json:"access_token"`
			Email       string `json:"email"`
		}
		data, err := os.ReadFile(tokenFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(data, &tokens)).To(Succeed())
		Expect(tokens[cli.host].AccessToken).NotTo(BeEmpty())
		Expect(tokens[cli.host].Email).To(BeEmpty(), "the key identifies the account by itself")
	})

	It("signs in lazily for clone through NIPA_SSH_KEY", func() {
		dir := workspace()
		keyPath, publicKey := newSSHKeyPair(dir)

		key := api.addSSHKey("clone key", publicKey)
		DeferCleanup(func() { api.removeSSHKey(key.ID) })

		project := newProject("ssh-login")
		seedRepo(orgSlug, project, map[string][]byte{"readme.txt": []byte("ssh\n")}, "seed")

		tokenFile := filepath.Join(dir, "tokens.json")
		res := runNipaEnv(dir, []string{
			"NIPA_SSH_KEY=" + keyPath,
			"NIPA_TOKEN_FILE=" + tokenFile,
		}, "clone", repoURLFor(orgSlug, project), "work")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readText(filepath.Join(dir, "work"), "readme.txt")).To(Equal("ssh\n"))

		data, err := os.ReadFile(tokenFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).NotTo(BeEmpty(), "the lazy login stored a session for the host")
	})

	It("rejects an unregistered key without a password fallback", func() {
		dir := workspace()
		keyPath, _ := newSSHKeyPair(dir)

		tokenFile := filepath.Join(dir, "tokens.json")
		res := runNipaEnv(dir, []string{"NIPA_TOKEN_FILE=" + tokenFile},
			"login", "--host", cli.host, "--ssh-key", keyPath)
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("invalid email or SSH key"))
		Expect(fileExists(dir, "tokens.json")).To(BeFalse(), "no session is stored for a rejected key")
	})

	It("binds an explicit username to the key owner", func() {
		dir := workspace()
		keyPath, publicKey := newSSHKeyPair(dir)

		key := api.addSSHKey("bound key", publicKey)
		DeferCleanup(func() { api.removeSSHKey(key.ID) })

		res := runNipaEnv(dir, []string{"NIPA_TOKEN_FILE=" + filepath.Join(dir, "tokens.json")},
			"login", "--host", cli.host, "--username", "someone-else@example.com", "--ssh-key", keyPath)
		Expect(res.ExitCode).NotTo(Equal(0))
		Expect(res.Output()).To(ContainSubstring("invalid email or SSH key"))
	})
})
