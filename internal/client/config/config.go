package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/obs"
)

// EnvDiffExternal names the environment variable holding an external diff
// command, mirroring git's GIT_EXTERNAL_DIFF.
const EnvDiffExternal = "NIPA_EXTERNAL_DIFF"

// EnvUploadWorkers names the environment variable pinning the number of
// concurrent chunk uploads. When unset, upload concurrency is tuned
// automatically within the client's bounds.
const EnvUploadWorkers = "NIPA_UPLOAD_WORKERS"

// EnvTracing enables client tracing (1/true/on or "stdout" for the pretty
// human-readable exporter). Unset or false leaves the client on the no-op
// tracer.
const EnvTracing = "NIPA_TRACING"

// EnvTracingOTLPEndpoint overrides the OTLP gRPC collector endpoint.
const EnvTracingOTLPEndpoint = "NIPA_TRACING_OTLP_ENDPOINT"

// EnvTracingSampleRatio sets the trace sampling ratio in (0, 1].
const EnvTracingSampleRatio = "NIPA_TRACING_SAMPLE_RATIO"

// LLM environment overrides for --generate. NIPA_LLM_API_KEY holds the key
// itself; the config file only names the variable that holds it.
const (
	EnvLLMEndpoint = "NIPA_LLM_ENDPOINT"
	EnvLLMModel    = "NIPA_LLM_MODEL"
	EnvLLMAPIKey   = "NIPA_LLM_API_KEY"
)

// Config is the user configuration stored in ~/.config/nipa/config.json.
type Config struct {
	DiffExternal  string     `json:"diffExternal,omitempty"`
	UploadWorkers int        `json:"uploadWorkers,omitempty"`
	SSHKeyPath    string     `json:"sshKeyPath,omitempty"`
	LLM           *LLMConfig `json:"llm,omitempty"`
}

// LLMConfig points --generate at an OpenAI-compatible endpoint. APIKeyEnv
// names the environment variable holding the key; keys are never stored in
// the file.
type LLMConfig struct {
	Endpoint  string `json:"endpoint,omitempty"`
	Model     string `json:"model,omitempty"`
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
}

// LLMSettings is the resolved endpoint, model and key.
type LLMSettings struct {
	Endpoint string
	Model    string
	APIKey   string
}

// ResolveLLM merges the llm config block with the environment, which takes
// precedence. The key comes from NIPA_LLM_API_KEY, else from the variable
// named by apiKeyEnv. Empty fields mean generation is not configured.
func ResolveLLM() (LLMSettings, error) {
	cfg, err := Load()
	if err != nil {
		return LLMSettings{}, err
	}
	var out LLMSettings
	keyEnv := ""
	if cfg.LLM != nil {
		out.Endpoint = strings.TrimSpace(cfg.LLM.Endpoint)
		out.Model = strings.TrimSpace(cfg.LLM.Model)
		keyEnv = strings.TrimSpace(cfg.LLM.APIKeyEnv)
	}
	if v := strings.TrimSpace(os.Getenv(EnvLLMEndpoint)); v != "" {
		out.Endpoint = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvLLMModel)); v != "" {
		out.Model = v
	}
	out.APIKey = strings.TrimSpace(os.Getenv(EnvLLMAPIKey))
	if out.APIKey == "" && keyEnv != "" {
		out.APIKey = strings.TrimSpace(os.Getenv(keyEnv))
	}
	return out, nil
}

// Path returns the user configuration file location.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nipa", "config.json"), nil
}

// Load reads the user configuration. A missing file yields an empty config.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// ResolveTracing builds the client tracing configuration from the environment.
// Tracing stays off unless NIPA_TRACING is set to a truthy value or "stdout".
func ResolveTracing() (obs.TracingConfig, error) {
	var cfg obs.TracingConfig
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvTracing))) {
	case "", "0", "false", "off", "no":
		return cfg, nil
	case "stdout":
		cfg.Enabled = true
		cfg.Stdout = true
	default:
		cfg.Enabled = true
	}
	cfg.Endpoint = strings.TrimSpace(os.Getenv(EnvTracingOTLPEndpoint))
	if value := strings.TrimSpace(os.Getenv(EnvTracingSampleRatio)); value != "" {
		ratio, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse %s: %w", EnvTracingSampleRatio, err)
		}
		if ratio <= 0 || ratio > 1 {
			return cfg, fmt.Errorf("%s must be in (0, 1]", EnvTracingSampleRatio)
		}
		cfg.SampleRatio = ratio
	}
	return cfg, nil
}

// ResolveDiffExternal returns the configured external diff command, with the
// environment taking precedence over the user config.
func ResolveDiffExternal() (string, error) {
	if value := strings.TrimSpace(os.Getenv(EnvDiffExternal)); value != "" {
		return value, nil
	}
	cfg, err := Load()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.DiffExternal), nil
}

// ResolveUploadWorkers returns the pinned upload worker count. The boolean is
// false when neither the environment nor the config file pins a value, in
// which case the client tunes concurrency automatically. The environment takes
// precedence over the user config.
func ResolveUploadWorkers() (int, bool, error) {
	if value := strings.TrimSpace(os.Getenv(EnvUploadWorkers)); value != "" {
		workers, err := strconv.Atoi(value)
		if err != nil {
			return 0, false, fmt.Errorf("parse %s: %w", EnvUploadWorkers, err)
		}
		if workers <= 0 {
			return 0, false, fmt.Errorf("%s must be a positive integer", EnvUploadWorkers)
		}
		return workers, true, nil
	}
	cfg, err := Load()
	if err != nil {
		return 0, false, err
	}
	if cfg.UploadWorkers > 0 {
		return cfg.UploadWorkers, true, nil
	}
	return 0, false, nil
}

// SetUploadWorkers pins the upload worker count in the user configuration; 0
// removes the pin so concurrency is tuned automatically. Other keys are kept.
func SetUploadWorkers(workers int) error {
	if workers < 0 {
		return fmt.Errorf("upload workers must not be negative")
	}
	return update(func(raw map[string]json.RawMessage) error {
		if workers == 0 {
			delete(raw, "uploadWorkers")
		} else {
			raw["uploadWorkers"] = json.RawMessage(strconv.Itoa(workers))
		}
		return nil
	})
}

// SetLLM stores the generation endpoint and model, keeping apiKeyEnv and
// every other key. Both empty removes them. A key is never written here.
func SetLLM(endpoint, model string) error {
	endpoint, model = strings.TrimSpace(endpoint), strings.TrimSpace(model)
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("the endpoint must be an http(s) URL such as http://localhost:11434/v1")
		}
	}
	return update(func(raw map[string]json.RawMessage) error {
		block := LLMConfig{}
		if data, ok := raw["llm"]; ok {
			if err := json.Unmarshal(data, &block); err != nil {
				return fmt.Errorf("parse llm: %w", err)
			}
		}
		block.Endpoint, block.Model = endpoint, model
		if block == (LLMConfig{}) {
			delete(raw, "llm")
			return nil
		}
		data, err := json.Marshal(block)
		if err != nil {
			return err
		}
		raw["llm"] = data
		return nil
	})
}

func update(edit func(raw map[string]json.RawMessage) error) error {
	path, err := Path()
	if err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if err := edit(raw); err != nil {
		return err
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
