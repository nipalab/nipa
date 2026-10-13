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
	storage        secureStorage
	login          refreshExecutor
	input          userInput
	resolveSigners signerResolver
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

// WithSSHLogin enables SSH key re-authentication with the given signer resolver.
func (s *Session) WithSSHLogin(resolve signerResolver) *Session {
	s.resolveSigners = resolve
	return s
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

	refreshed.Email = loginResult.Email
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
	if token, ok := s.trySSHReauth(ctx, host); ok {
		return token, nil
	}

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
	loginResult.Email = username

	err = s.storage.SaveToken(loginResult)
	if err != nil {
		return "", err
	}

	return loginResult.AccessToken, nil
}

func (s *Session) trySSHReauth(ctx context.Context, host string) (string, bool) {
	if s.resolveSigners == nil {
		return "", false
	}
	sshLogin, ok := s.login.(sshLoginExecutor)
	if !ok {
		return "", false
	}
	stored, err := s.storage.LoadToken(host)
	if err != nil || stored.Email == "" {
		return "", false
	}
	signers, err := s.resolveSigners()
	if err != nil || len(signers) == 0 {
		return "", false
	}
	loginResult, err := loginWithSSHKey(ctx, sshLogin, host, stored.Email, signers)
	if err != nil {
		return "", false
	}
	loginResult.Email = stored.Email
	if err := s.storage.SaveToken(loginResult); err != nil {
		return "", false
	}
	return loginResult.AccessToken, true
}
