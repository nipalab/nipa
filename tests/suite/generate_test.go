package suite_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa push --generate", func() {
	It("pushes a commit message drafted by an OpenAI-compatible endpoint", func() {
		var (
			mu     sync.Mutex
			prompt string
			auth   string
		)
		llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/v1/chat/completions"))
			body, err := io.ReadAll(r.Body)
			Expect(err).NotTo(HaveOccurred())
			mu.Lock()
			prompt, auth = string(body), r.Header.Get("Authorization")
			mu.Unlock()
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"feat: add generated file"}}]}`)
		}))
		defer llm.Close()

		project := newProject("generate-push")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")
		writeText(dir, "g.txt", "generated\n")
		Expect(runNipa(dir, "add", "g.txt").ExitCode).To(Equal(0))

		env := []string{"NIPA_LLM_ENDPOINT=" + llm.URL + "/v1", "NIPA_LLM_MODEL=e2e-model", "NIPA_LLM_API_KEY=sk-e2e"}
		refused := runNipaEnv(dir, env, "push", "--generate")
		Expect(refused.ExitCode).NotTo(Equal(0))
		Expect(refused.Output()).To(ContainSubstring("pass --yes"))

		pushed := runNipaEnv(dir, env, "push", "--generate", "--yes")
		Expect(pushed.ExitCode).To(Equal(0), pushed.Output())
		Expect(pushed.Output()).NotTo(ContainSubstring("sk-e2e"))

		mu.Lock()
		Expect(auth).To(Equal("Bearer sk-e2e"))
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		Expect(json.Unmarshal([]byte(prompt), &req)).To(Succeed())
		mu.Unlock()
		Expect(req.Messages).To(HaveLen(2))
		Expect(req.Messages[1].Content).To(ContainSubstring("+generated"))

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		Expect(strings.TrimSpace(log.Commits[0].Message)).To(Equal("feat: add generated file\n\nNipa-Generated: e2e-model"))
	})
})
