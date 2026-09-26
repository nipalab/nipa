package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// TokenHeader carries the daemon capability token on every RPC.
const TokenHeader = "x-nipa-daemon-token"

// Version is the daemon version reported by Ping.
var Version = "dev"

// Endpoint is the discovery record stored in ~/.config/nipa/daemon.json.
type Endpoint struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Version string `json:"version"`
}

// EndpointPath returns the daemon discovery file location.
func EndpointPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nipa", "daemon.json"), nil
}

func newToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// WriteEndpoint atomically publishes the endpoint record with 0600 permissions.
func WriteEndpoint(path string, ep Endpoint) error {
	data, err := json.Marshal(ep)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o600)
}

// ReadEndpoint reads the discovery record. A missing file yields os.ErrNotExist.
func ReadEndpoint(path string) (*Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ep Endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &ep, nil
}

// RemoveEndpoint deletes the discovery record only if it still holds token, so
// a daemon that lost a race never removes its successor's record.
func RemoveEndpoint(path, token string) error {
	ep, err := ReadEndpoint(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !VerifyToken(token, ep.Token) {
		return nil
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// VerifyToken compares the presented daemon token in constant time.
func VerifyToken(expected, presented string) bool {
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}

// ProcessAlive reports whether pid belongs to a running process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".daemon-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
