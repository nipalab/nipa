package securestorage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/nipalab/nipa/internal/client/domain"
)

func useKeyring(t *testing.T) {
	t.Helper()
	t.Setenv(TokenFileEnv, "")
}

func TestStorage_SaveAndLoadToken(t *testing.T) {
	useKeyring(t)
	keyring.MockInit()
	s := New()

	data := &domain.LoginResult{
		Host:         "example.com",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresIn:    1800,
	}

	require.NoError(t, s.SaveToken(data))

	got, err := s.LoadToken("example.com")
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestStorage_SaveToken_KeyringError(t *testing.T) {
	useKeyring(t)
	keyring.MockInitWithError(errors.New("keyring failure"))
	s := New()

	err := s.SaveToken(&domain.LoginResult{
		Host:         "example.com",
		AccessToken:  "access",
		RefreshToken: "refresh",
	})
	require.Error(t, err)
}

func TestStorage_LoadToken_NotFound(t *testing.T) {
	useKeyring(t)
	keyring.MockInit()
	s := New()

	_, err := s.LoadToken("unknown.example.com")
	require.Error(t, err)
	require.Equal(t, keyring.ErrNotFound, err)
}

func TestStorage_LoadToken_KeyringError(t *testing.T) {
	useKeyring(t)
	keyring.MockInitWithError(errors.New("keyring failure"))
	s := New()

	_, err := s.LoadToken("example.com")
	require.Error(t, err)
}

func TestStorage_LoadToken_InvalidJSON(t *testing.T) {
	useKeyring(t)
	keyring.MockInit()

	require.NoError(t, keyring.Set(serviceName, "example.com", "not-valid-json"))
	_, err := New().LoadToken("example.com")
	require.Error(t, err)
}

func TestStorage_LoadToken_Empty(t *testing.T) {
	useKeyring(t)
	keyring.MockInit()

	require.NoError(t, keyring.Set(serviceName, "example.com", "{}"))

	got, err := New().LoadToken("example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got.AccessToken)
}

func TestStorage_TokenFile_SaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	t.Setenv(TokenFileEnv, path)
	s := New()

	data := &domain.LoginResult{
		Host:         "127.0.0.1:6745",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresIn:    1800,
	}
	require.NoError(t, s.SaveToken(data))

	got, err := s.LoadToken("127.0.0.1:6745")
	require.NoError(t, err)
	require.Equal(t, data, got)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestStorage_TokenFile_KeepsOtherHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	t.Setenv(TokenFileEnv, path)
	s := New()

	require.NoError(t, s.SaveToken(&domain.LoginResult{Host: "a.example.com", AccessToken: "a"}))
	require.NoError(t, s.SaveToken(&domain.LoginResult{Host: "b.example.com", AccessToken: "b"}))
	require.NoError(t, s.SaveToken(&domain.LoginResult{Host: "a.example.com", AccessToken: "a2"}))

	a, err := s.LoadToken("a.example.com")
	require.NoError(t, err)
	require.Equal(t, "a2", a.AccessToken)

	b, err := s.LoadToken("b.example.com")
	require.NoError(t, err)
	require.Equal(t, "b", b.AccessToken)
}

func TestStorage_TokenFile_MissingHost(t *testing.T) {
	t.Setenv(TokenFileEnv, filepath.Join(t.TempDir(), "tokens.json"))

	_, err := New().LoadToken("unknown.example.com")
	require.ErrorIs(t, err, keyring.ErrNotFound)
}

func TestStorage_TokenFile_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	require.NoError(t, os.WriteFile(path, []byte("not-json"), 0o600))
	t.Setenv(TokenFileEnv, path)

	_, err := New().LoadToken("example.com")
	require.Error(t, err)

	err = New().SaveToken(&domain.LoginResult{Host: "example.com"})
	require.Error(t, err)
}

func TestStorage_TokenFile_SaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "tokens.json")
	t.Setenv(TokenFileEnv, path)

	require.NoError(t, New().SaveToken(&domain.LoginResult{Host: "example.com", AccessToken: "a"}))

	_, err := os.Stat(path)
	require.NoError(t, err)
}
