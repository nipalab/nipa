package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte(body), 0o644))
}

func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvLLMEndpoint, EnvLLMModel, EnvLLMAPIKey, "TEST_LLM_KEY"} {
		t.Setenv(k, "")
	}
}

func TestResolveLLM_Unset(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := ResolveLLM()
	require.NoError(t, err)
	require.Equal(t, LLMSettings{}, got)
}

func TestResolveLLM_FromConfig(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, `{"llm":{"endpoint":" http://localhost:11434/v1 ","model":"qwen","apiKeyEnv":"TEST_LLM_KEY"}}`)
	t.Setenv("TEST_LLM_KEY", "secret")
	got, err := ResolveLLM()
	require.NoError(t, err)
	require.Equal(t, LLMSettings{Endpoint: "http://localhost:11434/v1", Model: "qwen", APIKey: "secret"}, got)
}

func TestResolveLLM_EnvWins(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, `{"llm":{"endpoint":"http://a/v1","model":"m1","apiKeyEnv":"TEST_LLM_KEY"}}`)
	t.Setenv("TEST_LLM_KEY", "from-config-var")
	t.Setenv(EnvLLMEndpoint, "http://b/v1")
	t.Setenv(EnvLLMModel, "m2")
	t.Setenv(EnvLLMAPIKey, "direct")
	got, err := ResolveLLM()
	require.NoError(t, err)
	require.Equal(t, LLMSettings{Endpoint: "http://b/v1", Model: "m2", APIKey: "direct"}, got)
}

func TestResolveLLM_InvalidConfig(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "{")
	_, err := ResolveLLM()
	require.ErrorContains(t, err, "parse")
}
