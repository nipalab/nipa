package localrepo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/client/domain"
)

const (
	ConfigDir      = ".nipa"
	ConfigFile     = "config"
	DBFile         = "nipa.db"
	ObjectsDir     = "objects"
	MaxSearchDepth = 32
)

func (l *LocalRepo) configPath() string {
	return filepath.Join(l.target, ConfigDir, ConfigFile)
}

func (l *LocalRepo) SaveConfig(cfg domain.Config) error {
	if l.target == "" {
		return errors.New("local repo not initialized")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(l.configPath(), data, 0o644)
}

func (l *LocalRepo) LoadConfig() (*domain.Config, error) {
	if l.target == "" {
		return nil, errors.New("local repo not initialized")
	}
	data, err := os.ReadFile(l.configPath())
	if err != nil {
		return nil, err
	}
	var cfg domain.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func FindRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return FindRepoRootFrom(dir)
}

// FindRepoRootFrom searches dir and up to MaxSearchDepth parent directories for
// a .nipa/config file. A relative dir is resolved against the process working
// directory first, so callers can pass "." or a relative tool argument.
func FindRepoRootFrom(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for i := 0; i < MaxSearchDepth; i++ {
		configPath := filepath.Join(dir, ConfigDir, ConfigFile)
		if _, err := os.Stat(configPath); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", domain.NewUserError("not a nipa repository (or any of the parent directories)").
		WithHint("run this command inside a nipa clone", "nipa clone <url> <target>")
}
