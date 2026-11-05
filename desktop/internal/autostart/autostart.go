// Package autostart registers the desktop app to start when the user logs in.
package autostart

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

// Arg is passed to the app when the login item starts it, so it opens in the
// tray instead of showing its window.
const Arg = "--background"

const label = "com.nipalab.nipa"

var ErrUnsupported = errors.New("starting at login is not supported on this system")

// Set enables or disables the login item for exe.
func Set(enabled bool, exe string) error {
	if enabled {
		return enable(exe)
	}
	return disable()
}

func desktopEntry(exe string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Nipa
Comment=Version control for game teams
Exec=%s %s
Icon=nipa-desktop
Terminal=false
X-GNOME-Autostart-enabled=true
`, quoteExec(exe), Arg)
}

func quoteExec(exe string) string {
	if !strings.ContainsAny(exe, " \t\"'\\$`") {
		return exe
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return `"` + r.Replace(exe) + `"`
}

func launchAgent(exe string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`, label, html.EscapeString(exe), Arg)
}

func runCommand(exe string) string {
	return `"` + exe + `" ` + Arg
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
