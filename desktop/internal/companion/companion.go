// Package companion installs the nipa CLI shipped inside Windows and Linux
// release builds next to the app binary. The updater replaces only the app
// executable there, so carrying the CLI inside it keeps the daemon the app
// starts on the same release. macOS bundles ship the CLI inside the .app
// instead and build without the `companion` tag.
package companion

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Name is the CLI file name for this platform.
func Name() string {
	if runtime.GOOS == "windows" {
		return "nipa.exe"
	}
	return "nipa"
}

// Bundled reports whether this build carries the CLI.
func Bundled() bool {
	return len(payload) > 0
}

// Install writes the bundled CLI into dir unless an identical copy is
// already there; it reports whether the file changed.
func Install(dir string) (bool, error) {
	return install(dir, payload)
}

func install(dir string, data []byte) (bool, error) {
	if len(data) == 0 {
		return false, nil
	}
	target := filepath.Join(dir, Name())
	aside := target + ".old"
	_ = os.Remove(aside)
	if same(target, data) {
		return false, nil
	}
	tmp, err := os.CreateTemp(dir, ".nipa-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := firstErr(werr, cerr, os.Chmod(tmpName, 0o755)); err != nil {
		_ = os.Remove(tmpName)
		return false, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		// A running daemon keeps nipa.exe mapped on Windows: it cannot be
		// replaced, but it can be moved aside.
		if os.Rename(target, aside) != nil || os.Rename(tmpName, target) != nil {
			_ = os.Remove(tmpName)
			return false, err
		}
	}
	return true, nil
}

func same(path string, data []byte) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() != int64(len(data)) {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	want := sha256.Sum256(data)
	return bytes.Equal(h.Sum(nil), want[:])
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
