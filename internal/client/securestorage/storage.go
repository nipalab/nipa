package securestorage

import (
	"encoding/json/v2"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/zalando/go-keyring"
)

const serviceName = "nipa"

// TokenFileEnv makes the store fall back to a file-backed token store, keyed by
// host. It keeps scripting and CI usable where no OS keyring is available.
const TokenFileEnv = "NIPA_TOKEN_FILE"

type Storage struct {
}

func New() *Storage {
	return &Storage{}
}

func (s *Storage) SaveToken(data *domain.LoginResult) error {
	token := AccessToken{
		Host:         data.Host,
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		ExpiresIn:    data.ExpiresIn,
	}
	if path := os.Getenv(TokenFileEnv); path != "" {
		return saveTokenFile(path, &token)
	}

	jsonData, err := json.Marshal(token)
	if err != nil {
		return err
	}

	err = keyring.Set(serviceName, data.Host, string(jsonData))
	if err != nil {
		return err
	}

	return nil
}

func (s *Storage) LoadToken(host string) (*domain.LoginResult, error) {
	var token AccessToken
	if path := os.Getenv(TokenFileEnv); path != "" {
		fromFile, err := loadTokenFile(path, host)
		if err != nil {
			return nil, err
		}
		token = *fromFile
	} else {
		data, err := keyring.Get(serviceName, host)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &token); err != nil {
			return nil, err
		}
	}

	return &domain.LoginResult{
		Host:         token.Host,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresIn:    token.ExpiresIn,
	}, nil
}

func saveTokenFile(path string, token *AccessToken) error {
	tokens, err := readTokenFile(path)
	if err != nil {
		return err
	}
	tokens[token.Host] = *token

	data, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func loadTokenFile(path, host string) (*AccessToken, error) {
	tokens, err := readTokenFile(path)
	if err != nil {
		return nil, err
	}
	token, ok := tokens[host]
	if !ok {
		return nil, keyring.ErrNotFound
	}
	return &token, nil
}

func readTokenFile(path string) (map[string]AccessToken, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]AccessToken{}, nil
		}
		return nil, err
	}
	tokens := map[string]AccessToken{}
	if len(data) == 0 {
		return tokens, nil
	}
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tokens-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
