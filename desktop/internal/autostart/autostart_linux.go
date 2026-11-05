package autostart

import (
	"os"
	"path/filepath"
)

func entryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", "nipa-desktop.desktop"), nil
}

func enable(exe string) error {
	path, err := entryPath()
	if err != nil {
		return err
	}
	return writeFile(path, desktopEntry(exe))
}

func disable() error {
	path, err := entryPath()
	if err != nil {
		return err
	}
	return removeFile(path)
}
