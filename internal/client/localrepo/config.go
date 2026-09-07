package localrepo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/client/domain"
)

const (
	ConfigDir  = ".nipa"
	ConfigFile = "config"
	DBFile     = "nipa.db"
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
