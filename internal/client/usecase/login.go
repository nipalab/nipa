package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/sshkey"
	protocol "github.com/nipalab/nipa/internal/sshkey"
)

type loginExecutor interface {
	LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error)
}

type sshLoginExecutor interface {
	BeginLoginSSHKey(ctx context.Context, host, email string) (*domain.SSHKeyChallenge, error)
	CompleteLoginSSHKey(ctx context.Context, host, email, nonce string, publicKey, signature []byte) (*domain.LoginResult, error)
}

type userInput interface {
	PromptUsernameAndPassword() (username, password string, err error)
}

type usernameInput interface {
	PromptUsername() (string, error)
}

type passwordInput interface {
	PromptPassword() (string, error)
}

type secureStorage interface {
	SaveToken(data *domain.LoginResult) error
	LoadToken(host string) (*domain.LoginResult, error)
}

// signerResolver returns the SSH signers to try for a login, in priority order.
type signerResolver func() ([]sshkey.Signer, error)

type Auth struct {
	loginExecutor  loginExecutor
	secureStorage  secureStorage
	userInput      userInput
	resolveSigners signerResolver
}

func NewAuth(executor loginExecutor, storage secureStorage, input userInput) *Auth {
	return &Auth{
		loginExecutor: executor,
		secureStorage: storage,
		userInput:     input,
	}
}

// WithSSHLogin enables SSH key login with the given signer resolver.
func (l *Auth) WithSSHLogin(resolve signerResolver) *Auth {
	l.resolveSigners = resolve
	return l
}

func (l *Auth) LoginWithUsernamePassword(ctx context.Context, host, username, password string) error {
	if username == "" {
		return domain.NewUserError("username cannot be empty")
	}

	loginResult, err := l.loginExecutor.LoginWithUsernamePassword(ctx, host, username, password)
	if err != nil {
		return err
	}
	loginResult.Email = username

	err = l.secureStorage.SaveToken(loginResult)
	if err != nil {
		return err
	}

	return nil
}

func (l *Auth) LoginWithSSHKey(ctx context.Context, host, email string, signers []sshkey.Signer) error {
	if email == "" {
		return domain.NewUserError("username cannot be empty")
	}
	if len(signers) == 0 {
		return domain.NewUserError("no SSH key available")
	}
	executor, ok := l.loginExecutor.(sshLoginExecutor)
	if !ok {
		return domain.NewUserError("SSH key login is not supported by this client")
	}

	loginResult, err := loginWithSSHKey(ctx, executor, host, email, signers)
	if err != nil {
		return err
	}
	loginResult.Email = email

	return l.secureStorage.SaveToken(loginResult)
}

// loginWithSSHKey runs the challenge-response login, trying each signer until
// one is accepted; a rejected key moves on to the next, while transport or
// server failures stop the attempt.
func loginWithSSHKey(ctx context.Context, executor sshLoginExecutor, host, email string, signers []sshkey.Signer) (*domain.LoginResult, error) {
	var lastErr error
	for _, signer := range signers {
		challenge, err := executor.BeginLoginSSHKey(ctx, host, email)
		if err != nil {
			return nil, err
		}
		signature, err := signer.SignPayload(protocol.LoginPayload(challenge.Nonce))
		if err != nil {
			lastErr = err
			continue
		}
		result, err := executor.CompleteLoginSSHKey(ctx, host, email, challenge.Nonce, signer.PublicKey().Marshal(), signature)
		if err != nil {
			var domErr *domain.Error
			if errors.As(err, &domErr) && domErr.Code == 400 {
				lastErr = err
				continue
			}
			return nil, err
		}
		return result, nil
	}
	if lastErr == nil {
		lastErr = domain.NewUserError("no SSH key available for login")
	}
	return nil, lastErr
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
		return l.loginInteractively(ctx, host)
	}
	return nil
}

func (l *Auth) loginInteractively(ctx context.Context, host string) error {
	usernamePrompt, hasUsername := l.userInput.(usernameInput)
	passwordPrompt, hasPassword := l.userInput.(passwordInput)
	if l.resolveSigners != nil && hasUsername && hasPassword {
		username, err := usernamePrompt.PromptUsername()
		if err != nil {
			return err
		}
		if signers, err := l.resolveSigners(); err == nil && len(signers) > 0 {
			if err := l.LoginWithSSHKey(ctx, host, username, signers); err == nil {
				return nil
			}
		}
		password, err := passwordPrompt.PromptPassword()
		if err != nil {
			return err
		}
		return l.LoginWithUsernamePassword(ctx, host, username, password)
	}

	username, password, err := l.userInput.PromptUsernameAndPassword()
	if err != nil {
		return err
	}
	return l.LoginWithUsernamePassword(ctx, host, username, password)
}
