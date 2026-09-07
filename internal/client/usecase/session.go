package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
)

type refreshExecutor interface {
	LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error)
}

type Session struct {
	storage secureStorage
	login   refreshExecutor
}

func NewSession(storage secureStorage, login refreshExecutor) *Session {
	return &Session{
		storage: storage,
		login:   login,
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
		return "", domain.NewUserError("no refresh token stored")
	}

	refreshed, err := s.login.LoginWithRefreshToken(ctx, host, loginResult.RefreshToken)
	if err != nil {
		return "", err
	}

	err = s.storage.SaveToken(refreshed)
	if err != nil {
		return "", err
	}

	return refreshed.AccessToken, nil
}