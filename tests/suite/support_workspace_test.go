package suite_test

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func workspace() string {
	GinkgoHelper()
	return GinkgoT().TempDir()
}

func writeText(dir, path, content string) {
	GinkgoHelper()
	writeBytes(dir, path, []byte(content))
}

func writeBytes(dir, path string, data []byte) {
	GinkgoHelper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
	Expect(os.WriteFile(full, data, 0o644)).To(Succeed())
}

func readText(dir, path string) string {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

func readBytes(dir, path string) []byte {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
	Expect(err).NotTo(HaveOccurred())
	return data
}

func removePath(dir, path string) {
	GinkgoHelper()
	Expect(os.Remove(filepath.Join(dir, filepath.FromSlash(path)))).To(Succeed())
}

func fileExists(dir, path string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path)))
	return err == nil
}

func randomBytes(n int) []byte {
	GinkgoHelper()
	data := make([]byte, n)
	_, err := rand.Read(data)
	Expect(err).NotTo(HaveOccurred())
	return data
}

type repoConfig struct {
	URL    string   `json:"url"`
	Branch string   `json:"branch"`
	Sparse []string `json:"sparse"`
	Head   *struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"head"`
}

func loadRepoConfig(dir string) repoConfig {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join(dir, ".nipa", "config"))
	Expect(err).NotTo(HaveOccurred())
	var cfg repoConfig
	Expect(json.Unmarshal(data, &cfg)).To(Succeed())
	return cfg
}

func assertCleanStatus(dir string) {
	GinkgoHelper()
	res := runNipa(dir, "status")
	Expect(res.ExitCode).To(Equal(0), res.Stderr)
	Expect(res.Output()).To(ContainSubstring("On branch main"))
	Expect(res.Output()).NotTo(MatchRegexp(`(?m)^[ADM?!C]  `), res.Output())
}

func cloneRepo(parent, url, target string, extraArgs ...string) string {
	GinkgoHelper()
	args := append([]string{"clone", url, target}, extraArgs...)
	res := runNipa(parent, args...)
	Expect(res.ExitCode).To(Equal(0), "clone failed: %s%s", res.Stdout, res.Stderr)
	dir := filepath.Join(parent, target)
	Expect(fileExists(dir, ".nipa/config")).To(BeTrue(), ".nipa/config missing after clone")
	return dir
}

func pushRepo(dir, message string) commandResult {
	GinkgoHelper()
	return runNipa(dir, "push", "-m", uniqueMessage(message))
}
