package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "daemon.json")
	want := Endpoint{PID: 42, Port: 1234, Token: "abc", Version: "test"}
	require.NoError(t, WriteEndpoint(path, want))

	got, err := ReadEndpoint(path)
	require.NoError(t, err)
	require.Equal(t, want, *got)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestReadEndpointMissing(t *testing.T) {
	_, err := ReadEndpoint(filepath.Join(t.TempDir(), "daemon.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRemoveEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, WriteEndpoint(path, Endpoint{Token: "mine"}))

	require.NoError(t, RemoveEndpoint(path, "other"))
	_, err := ReadEndpoint(path)
	require.NoError(t, err, "a foreign token must not remove the record")

	require.NoError(t, RemoveEndpoint(path, "mine"))
	_, err = ReadEndpoint(path)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, RemoveEndpoint(path, "mine"), "removing a missing record is a no-op")
}

func TestVerifyToken(t *testing.T) {
	require.True(t, VerifyToken("secret", "secret"))
	require.False(t, VerifyToken("secret", "secrez"))
	require.False(t, VerifyToken("secret", ""))
}

func TestProcessAlive(t *testing.T) {
	require.True(t, ProcessAlive(os.Getpid()))
	require.False(t, ProcessAlive(-1))
	require.False(t, ProcessAlive(0))
	require.False(t, ProcessAlive(999999999))
}
