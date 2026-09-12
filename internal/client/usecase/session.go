package usecase

import (
	"context"
	"errors"

	"github.com/nipalab/nipa/internal/client/domain"
)

type refreshExecutor interface {
	LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error)
}

type Session struct {
	storage secureStorage
	login   refreshExecutor
	input   userInput
}

func NewSession(storage secureStorage, login refreshExecutor, inputs ...userInput) *Session {
	var input userInput
	if len(inputs) > 0 {
		input = inputs[0]
	}
	return &Session{
		storage: storage,
		login:   login,
		input:   input,
	}
}

func (s *Session) AccessToken(ctx context.Context, host string) (string, error) {
	loginResult, err := s.storage.LoadToken(host)
	if err != nil {
		return "", err
	}
	return loginResult.AccessToken, nil
}

func (s *Session) Refresh(ctx context.Context, host string) (string, error) {
	loginResult, err := s.storage.LoadToken(host)
	if err != nil {
		return "", err
	}
	if loginResult.RefreshToken == "" {
		return s.reauth(ctx, host, domain.NewUserError("no refresh token stored"))
	}

	refreshed, err := s.login.LoginWithRefreshToken(ctx, host, loginResult.RefreshToken)
	if err != nil {
		if !isRefreshRejection(err) {
			return "", err
		}
		return s.reauth(ctx, host, err)
	}

	err = s.storage.SaveToken(refreshed)
	if err != nil {
		return "", err
	}

	return refreshed.AccessToken, nil
}

func isRefreshRejection(err error) bool {
	var domErr *domain.Error
	return errors.As(err, &domErr)
}

func (s *Session) reauth(ctx context.Context, host string, cause error) (string, error) {
	passwordLogin, ok := s.login.(loginExecutor)
	if !ok || s.input == nil {
		return "", cause
	}

	username, password, err := s.input.PromptUsernameAndPassword()
	if err != nil {
		return "", err
	}
	if username == "" {
		return "", domain.NewUserError("username cannot be empty")
	}

	loginResult, err := passwordLogin.LoginWithUsernamePassword(ctx, host, username, password)
	if err != nil {
		return "", err
	}

	err = s.storage.SaveToken(loginResult)
	if err != nil {
		return "", err
	}

	return loginResult.AccessToken, nil
}
