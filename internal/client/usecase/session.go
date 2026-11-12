package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
)

const (
	refreshReloadAttempts = 5
	refreshReloadDelay    = 100 * time.Millisecond
)

type refreshExecutor interface {
	LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error)
}

type Session struct {
	storage        secureStorage
	login          refreshExecutor
	input          userInput
	resolveSigners signerResolver
	wait           func(ctx context.Context, d time.Duration) error
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
		wait:    waitContext,
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// WithSSHLogin enables SSH key re-authentication with the given signer resolver.
func (s *Session) WithSSHLogin(resolve signerResolver) *Session {
	s.resolveSigners = resolve
	return s
}

// AccessToken returns the stored access token for the server address, which
// may be a bare host or an http(s) origin; tokens are keyed by bare host.
func (s *Session) AccessToken(ctx context.Context, address string) (string, error) {
	host := domain.HostForAddress(address)
	loginResult, err := s.storage.LoadToken(host)
	if err != nil {
		return "", err
	}
	return loginResult.AccessToken, nil
}

func (s *Session) Refresh(ctx context.Context, address string) (string, error) {
	host := domain.HostForAddress(address)
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
		if token, ok := s.rotatedElsewhere(ctx, host, loginResult, err); ok {
			return token, nil
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

// rotatedElsewhere picks up tokens another process sharing this storage saved
// after rotating the session first. The store is keyed by host only, so the
// stored pair is adopted only for the same session or the same account. A
// "not found" answer means a concurrent rotation may still be saving, so the
// store is polled briefly.
func (s *Session) rotatedElsewhere(ctx context.Context, host string, presented *domain.LoginResult, cause error) (string, bool) {
	attempts := 1
	var domErr *domain.Error
	if errors.As(cause, &domErr) && domErr.Code == 404 {
		attempts = refreshReloadAttempts
	}
	for i := range attempts {
		if i > 0 && s.wait(ctx, refreshReloadDelay) != nil {
			return "", false
		}
		stored, err := s.storage.LoadToken(host)
		if err != nil {
			return "", false
		}
		if stored.RefreshToken != "" && stored.RefreshToken != presented.RefreshToken && stored.AccessToken != "" && sameSession(presented, stored) {
			return stored.AccessToken, true
		}
	}
	return "", false
}

func sameSession(presented, stored *domain.LoginResult) bool {
	if presented.Email != "" && presented.Email == stored.Email {
		return true
	}
	id := refreshSessionID(presented.RefreshToken)
	return id != "" && id == refreshSessionID(stored.RefreshToken)
}

// refreshSessionID returns the session part (`<id>.<tag>`) of a session-shaped
// refresh token, or "" for legacy tokens.
func refreshSessionID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "." + parts[1]
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
	if err != nil {
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
