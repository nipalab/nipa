package config

import (
	"encoding/json"
	"errors"
	"fmt"
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

// Config is the user configuration stored in ~/.config/nipa/config.json.
type Config struct {
	DiffExternal  string `json:"diffExternal,omitempty"`
	UploadWorkers int    `json:"uploadWorkers,omitempty"`
	SSHKeyPath    string `json:"sshKeyPath,omitempty"`
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
