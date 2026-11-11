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

func TestLLMKey_Keyring(t *testing.T) {
	useKeyring(t)
	keyring.MockInit()
	s := New()

	key, err := s.LoadLLMKey()
	require.NoError(t, err)
	require.Empty(t, key)

	require.NoError(t, s.SaveLLMKey("  sk-1  "))
	key, err = s.LoadLLMKey()
	require.NoError(t, err)
	require.Equal(t, "sk-1", key)

	require.NoError(t, s.SaveLLMKey(""))
	key, err = s.LoadLLMKey()
	require.NoError(t, err)
	require.Empty(t, key)
	require.NoError(t, s.DeleteLLMKey())
}

func TestLLMKey_KeyringError(t *testing.T) {
	useKeyring(t)
	keyring.MockInitWithError(errors.New("locked"))
	s := New()
	_, err := s.LoadLLMKey()
	require.ErrorContains(t, err, "locked")
	require.Error(t, s.SaveLLMKey("k"))
	require.Error(t, s.DeleteLLMKey())
}

func TestLLMKey_TokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	t.Setenv(TokenFileEnv, path)
	s := New()

	require.NoError(t, s.DeleteLLMKey())
	key, err := s.LoadLLMKey()
	require.NoError(t, err)
	require.Empty(t, key)

	require.NoError(t, s.SaveToken(&domain.LoginResult{Host: "example.com", AccessToken: "a"}))
	require.NoError(t, s.SaveLLMKey("sk-file"))
	key, err = s.LoadLLMKey()
	require.NoError(t, err)
	require.Equal(t, "sk-file", key)

	require.NoError(t, s.DeleteLLMKey())
	key, err = s.LoadLLMKey()
	require.NoError(t, err)
	require.Empty(t, key)
	tok, err := s.LoadToken("example.com")
	require.NoError(t, err)
	require.Equal(t, "a", tok.AccessToken)

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	_, err = s.LoadLLMKey()
	require.Error(t, err)
	require.Error(t, s.DeleteLLMKey())
}
