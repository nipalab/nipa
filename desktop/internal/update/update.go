// Package update holds the desktop app's release identity and the rules
// around in-app updates: which builds may update, whether the running copy
// can be replaced in place, and the signed manifest published with each
// release (consumed by the Wails updater's endpoint provider).
package update

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"
)

// Set with -ldflags -X by release builds.
var (
	FeedURL   = "https://github.com/nipalab/nipa/releases/latest/download/update.json"
	PublicKey = ""
)

const EnvFeedURL = "NIPA_UPDATE_URL"

// Canonical returns v as "1.2.3" (no "v" prefix), or "" when v is not a
// release version (dev builds, pseudo-versions with a dirty suffix).
func Canonical(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) || strings.HasSuffix(v, "-dirty") {
		return ""
	}
	return strings.TrimPrefix(semver.Canonical(v), "v")
}

// Older reports whether have is a release version strictly older than want.
// Anything that is not a release version is never older.
func Older(have, want string) bool {
	h, w := Canonical(have), Canonical(want)
	if h == "" || w == "" {
		return false
	}
	return semver.Compare("v"+h, "v"+w) < 0
}

// Feed returns the manifest URL, honouring NIPA_UPDATE_URL.
func Feed() string {
	if u := strings.TrimSpace(os.Getenv(EnvFeedURL)); u != "" {
		return u
	}
	return FeedURL
}

// Key decodes the pinned ed25519 public key.
func Key() ([]byte, error) {
	if PublicKey == "" {
		return nil, errors.New("no update signing key in this build")
	}
	return base64.StdEncoding.DecodeString(PublicKey)
}

// Target is what an update replaces: the .app bundle on macOS, the
// executable elsewhere.
func Target(exe string) string {
	if runtime.GOOS != "darwin" {
		return exe
	}
	return bundleOf(exe)
}

func bundleOf(exe string) string {
	clean := filepath.Clean(exe)
	for dir := clean; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
	}
	return clean
}

// Replaceable reports whether target can be swapped in place: its directory
// must be writable by this user. Package-managed installs (/usr/bin, Program
// Files, a mounted AppImage) are not, so they only get a download link.
func Replaceable(target string) bool {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".nipa-update-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
