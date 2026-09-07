package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
)

type loginExecutor interface {
	LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error)
}

type userInput interface {
	PromptUsernameAndPassword() (username, password string, err error)
}

type secureStorage interface {
	SaveToken(data *domain.LoginResult) error
	LoadToken(host string) (*domain.LoginResult, error)
}

type Auth struct {
	loginExecutor loginExecutor
	secureStorage secureStorage
	userInput     userInput
}

func NewAuth(executor loginExecutor, storage secureStorage, input userInput) *Auth {
	return &Auth{
		loginExecutor: executor,
		secureStorage: storage,
		userInput:     input,
	}
}

func (l *Auth) LoginWithUsernamePassword(ctx context.Context, host, username, password string) error {
	if username == "" {
		return domain.NewUserError("username cannot be empty")
	}

	loginResult, err := l.loginExecutor.LoginWithUsernamePassword(ctx, host, username, password)
	if err != nil {
		return err
	}

	err = l.secureStorage.SaveToken(loginResult)
	if err != nil {
		return err
	}

	return nil
}

func (l *Auth) isLoggedIn(host string) (bool, error) {
	token, err := l.secureStorage.LoadToken(host)
	if err != nil {
		return false, nil
	}

	parts := strings.Split(token.AccessToken, ".")
	if len(parts) != 3 {
		return false, domain.NewTokenError("invalid token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false, domain.NewTokenError("invalid token payload")
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false, domain.NewTokenError("invalid token claims")
	}

	return true, nil
}

func (l *Auth) MakeSureLoggedIn(ctx context.Context, host string) error {
	if loggedIn, err := l.isLoggedIn(host); !loggedIn || err != nil {
		username, password, err := l.userInput.PromptUsernameAndPassword()
		if err != nil {
			return err
		}
		err = l.LoginWithUsernamePassword(ctx, host, username, password)
		if err != nil {
			return err
		}
	}
	return nil
}
