// Package config loads the user-level nipa client configuration.
//
// The file lives at os.UserConfigDir()/nipa/config.json
// (~/.config/nipa/config.json on Linux). It only holds machine-local
// preferences such as external tool commands; repository state stays in
// the clone's .nipa directory.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const EnvDiffTool = "NIPA_DIFF_TOOL"

type Config struct {
	DiffTool string `json:"diffTool,omitempty"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "nipa", "config.json"), nil
}

func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read user config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse user config %s: %w", p, err)
	}
	return c, nil
}

func ResolveDiffTool(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv(EnvDiffTool); env != "" {
		return env, nil
	}
	cfg, err := Load()
	if err != nil {
		return "", err
	}
	return cfg.DiffTool, nil
}
