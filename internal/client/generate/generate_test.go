package generate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/securestorage"
)

type stubLLM struct {
	answer  string
	err     error
	prompts []aiprompt.Prompt
}

func (s *stubLLM) Complete(_ context.Context, p aiprompt.Prompt) (string, error) {
	s.prompts = append(s.prompts, p)
	return s.answer, s.err
}

func (s *stubLLM) Model() string { return "stub-model" }

type stubSources struct {
	commit *aiprompt.CommitInput
	mr     *aiprompt.MRInput
	err    error
	args   []string
}

func (s *stubSources) Commit(_ context.Context, root string) (*aiprompt.CommitInput, error) {
	s.args = append(s.args, root)
	return s.commit, s.err
}

func (s *stubSources) MRDescription(_ context.Context, root, source, target string) (*aiprompt.MRInput, error) {
	s.args = append(s.args, root, source, target)
	return s.mr, s.err
}

type stubKeys struct {
	key   string
	err   error
	calls int
}

func (k *stubKeys) LoadLLMKey() (string, error) {
	k.calls++
	return k.key, k.err
}

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv(config.EnvLLMEndpoint, "")
	t.Setenv(config.EnvLLMModel, "")
	t.Setenv(config.EnvLLMAPIKey, "")
	t.Setenv("MY_LLM_KEY", "")
	t.Setenv(securestorage.TokenFileEnv, filepath.Join(dir, "tokens.json"))
	return dir
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "nipa", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func TestSettings_KeyOrder(t *testing.T) {
	dir := isolate(t)
	keys := &stubKeys{key: "from-keyring"}

	s, err := Settings(keys)
	require.NoError(t, err)
	require.Empty(t, s.APIKey)
	require.Zero(t, keys.calls)

	writeConfig(t, dir, `{"llm":{"endpoint":"http://x/v1","model":"m","apiKeyEnv":"MY_LLM_KEY"}}`)
	s, err = Settings(keys)
	require.NoError(t, err)
	require.Equal(t, "from-keyring", s.APIKey)

	t.Setenv("MY_LLM_KEY", "from-indirect")
	s, err = Settings(keys)
	require.NoError(t, err)
	require.Equal(t, "from-indirect", s.APIKey)

	t.Setenv(config.EnvLLMAPIKey, "from-env")
	s, err = Settings(keys)
	require.NoError(t, err)
	require.Equal(t, "from-env", s.APIKey)

	t.Setenv(config.EnvLLMAPIKey, "")
	t.Setenv("MY_LLM_KEY", "")
	s, err = Settings(&stubKeys{err: errors.New("locked")})
	require.NoError(t, err)
	require.Empty(t, s.APIKey)

	s, err = Settings(nil)
	require.NoError(t, err)
	require.Empty(t, s.APIKey)

	writeConfig(t, dir, `{`)
	_, err = Settings(keys)
	require.Error(t, err)
	_, err = NewLLM(keys)
	require.Error(t, err)
}

func TestNewLLM(t *testing.T) {
	dir := isolate(t)
	_, err := NewLLM(&stubKeys{})
	require.ErrorIs(t, err, ErrNotConfigured)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Contains(t, err.Error(), "no LLM endpoint configured")

	writeConfig(t, dir, `{"llm":{"endpoint":"ftp://x","model":"m"}}`)
	_, err = NewLLM(&stubKeys{})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotConfigured)

	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "OK"}}}})
	}))
	t.Cleanup(srv.Close)
	writeConfig(t, dir, `{"llm":{"endpoint":"`+srv.URL+`/v1","model":"m"}}`)
	require.NoError(t, securestorage.New().SaveLLMKey("sk-saved"))
	model, err := Default()
	require.NoError(t, err)
	require.Equal(t, "m", model.Model())
	require.NoError(t, Ping(context.Background(), model))
	require.Equal(t, "Bearer sk-saved", auth)

	g := &Generator{}
	model, err = g.Model()
	require.NoError(t, err)
	require.Equal(t, "m", model.Model())
}

func TestGenerator_Commit(t *testing.T) {
	model := &stubLLM{answer: "```\nfeat: add a\n```"}
	src := &stubSources{commit: &aiprompt.CommitInput{Branch: "main"}}
	g := &Generator{LLM: func() (LLM, error) { return model, nil }, Sources: func(string) (Sources, error) { return src, nil }}

	res, err := g.Commit(context.Background(), "/repo")
	require.NoError(t, err)
	require.Equal(t, &Result{Text: "feat: add a", Model: "stub-model"}, res)
	require.Equal(t, []string{"/repo"}, src.args)
	require.Len(t, model.prompts, 1)

	model.answer = " "
	_, err = g.Commit(context.Background(), "/repo")
	require.ErrorContains(t, err, "empty answer")

	model.err = errors.New("down")
	_, err = g.Commit(context.Background(), "/repo")
	require.ErrorContains(t, err, "down")

	src.err = errors.New("nothing staged")
	_, err = g.Commit(context.Background(), "/repo")
	require.ErrorContains(t, err, "nothing staged")

	g.LLM = func() (LLM, error) { return nil, ErrNotConfigured }
	_, err = g.Commit(context.Background(), "/repo")
	require.ErrorIs(t, err, ErrNotConfigured)

	g = &Generator{LLM: func() (LLM, error) { return model, nil }}
	_, err = g.Commit(context.Background(), "/repo")
	require.ErrorContains(t, err, "diff is not configured")
	_, err = g.MergeRequest(context.Background(), "/repo", "", "")
	require.ErrorContains(t, err, "diff is not configured")
}

func TestGenerator_MergeRequest(t *testing.T) {
	model := &stubLLM{answer: "# Add c\n\nSummary.\n\n- c added"}
	src := &stubSources{mr: &aiprompt.MRInput{Source: "feature", Target: "main"}}
	g := &Generator{LLM: func() (LLM, error) { return model, nil }, Sources: func(string) (Sources, error) { return src, nil }}

	res, err := g.MergeRequest(context.Background(), "/repo", "feature", "")
	require.NoError(t, err)
	require.Equal(t, &Result{Title: "Add c", Text: "Summary.\n\n- c added", Model: "stub-model", Source: "feature", Target: "main"}, res)
	require.Equal(t, []string{"/repo", "feature", ""}, src.args)

	model.answer = ""
	_, err = g.MergeRequest(context.Background(), "/repo", "feature", "main")
	require.ErrorContains(t, err, "empty answer")

	src.err = errors.New("must differ")
	_, err = g.MergeRequest(context.Background(), "/repo", "main", "main")
	require.ErrorContains(t, err, "must differ")

	g.LLM = func() (LLM, error) { return nil, errors.New("bad") }
	_, err = g.MergeRequest(context.Background(), "/repo", "", "")
	require.ErrorContains(t, err, "bad")
}
