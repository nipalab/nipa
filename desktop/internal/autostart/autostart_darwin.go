package autostart

import (
	"os"
	"path/filepath"
)

func agentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func enable(exe string) error {
	path, err := agentPath()
	if err != nil {
		return err
	}
	return writeFile(path, launchAgent(exe))
}

func disable() error {
	path, err := agentPath()
	if err != nil {
		return err
	}
	return removeFile(path)
}
