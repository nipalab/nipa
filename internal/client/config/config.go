package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvDiffExternal names the environment variable holding an external diff
// command, mirroring git's GIT_EXTERNAL_DIFF.
const EnvDiffExternal = "NIPA_EXTERNAL_DIFF"

// EnvUploadWorkers names the environment variable pinning the number of
// concurrent chunk uploads. When unset, upload concurrency is tuned
// automatically within the client's bounds.
const EnvUploadWorkers = "NIPA_UPLOAD_WORKERS"

// Config is the user configuration stored in ~/.config/nipa/config.json.
type Config struct {
	DiffExternal  string `json:"diffExternal,omitempty"`
	UploadWorkers int    `json:"uploadWorkers,omitempty"`
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
