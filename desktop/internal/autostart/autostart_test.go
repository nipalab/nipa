package autostart

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContent(t *testing.T) {
	require.Contains(t, desktopEntry("/opt/nipa/nipa-desktop"), "Exec=/opt/nipa/nipa-desktop --background\n")
	require.Contains(t, desktopEntry(`/opt/my apps/"nipa"$`), `Exec="/opt/my apps/\"nipa\"\$" --background`)
	plist := launchAgent("/Applications/Nipa & Co.app/Contents/MacOS/nipa-desktop")
	require.Contains(t, plist, "<string>/Applications/Nipa &amp; Co.app/Contents/MacOS/nipa-desktop</string>")
	require.Contains(t, plist, "<string>com.nipalab.nipa</string>")
	require.Equal(t, `"C:\Nipa\nipa.exe" --background`, runCommand(`C:\Nipa\nipa.exe`))
}

func TestSetWritesAndRemovesTheEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	require.NoError(t, Set(true, "/usr/bin/nipa-desktop"))
	path := filepath.Join(dir, "autostart", "nipa-desktop.desktop")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(dir, "Library", "LaunchAgents", "com.nipalab.nipa.plist")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "/usr/bin/nipa-desktop")
	require.NoError(t, Set(false, ""))
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, Set(false, ""), "disabling twice is fine")
}

func TestWriteAndRemoveErrors(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	require.Error(t, writeFile(filepath.Join(blocker, "x", "entry"), ""))
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "child"), nil, 0o600))
	require.Error(t, removeFile(dir))
}
