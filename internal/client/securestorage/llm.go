package securestorage

import (
	"encoding/json/v2"
	"errors"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// LLMKeyName is the keyring entry (and token file entry under
// NIPA_TOKEN_FILE) holding the API key of the generation endpoint.
const LLMKeyName = "llm-api-key"

// SaveLLMKey stores the generation API key; an empty key deletes it.
func (s *Storage) SaveLLMKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return s.DeleteLLMKey()
	}
	if path := os.Getenv(TokenFileEnv); path != "" {
		return saveTokenFile(path, &AccessToken{Host: LLMKeyName, AccessToken: key})
	}
	return keyring.Set(serviceName, LLMKeyName, key)
}

// LoadLLMKey returns the stored generation API key, or "" when none is saved.
func (s *Storage) LoadLLMKey() (string, error) {
	var key string
	var err error
	if path := os.Getenv(TokenFileEnv); path != "" {
		var token *AccessToken
		if token, err = loadTokenFile(path, LLMKeyName); err == nil {
			key = token.AccessToken
		}
	} else {
		key, err = keyring.Get(serviceName, LLMKeyName)
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(key), nil
}

// DeleteLLMKey removes the stored generation API key; a missing key is fine.
func (s *Storage) DeleteLLMKey() error {
	if path := os.Getenv(TokenFileEnv); path != "" {
		tokens, err := readTokenFile(path)
		if err != nil {
			return err
		}
		if _, ok := tokens[LLMKeyName]; !ok {
			return nil
		}
		delete(tokens, LLMKeyName)
		data, err := json.Marshal(tokens)
		if err != nil {
			return err
		}
		return atomicWrite(path, data)
	}
	err := keyring.Delete(serviceName, LLMKeyName)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
