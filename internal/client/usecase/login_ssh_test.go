package usecase

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/sshkey"
)

func testSSHSigner(t *testing.T, name string) sshkey.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return sshkey.Signer{Signer: signer, Name: name}
}

func testSigners(t *testing.T, names ...string) []sshkey.Signer {
	t.Helper()
	signers := make([]sshkey.Signer, 0, len(names))
	for _, name := range names {
		signers = append(signers, testSSHSigner(t, name))
	}
	return signers
}

type stubSSHExecutor struct {
	beginErr      error
	nonce         string
	results       []*domain.LoginResult
	completeErrs  []error
	completeCalls int
	lastEmail     string
}

func (s *stubSSHExecutor) BeginLoginSSHKey(_ context.Context, _, email string) (*domain.SSHKeyChallenge, error) {
	s.lastEmail = email
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	nonce := s.nonce
	if nonce == "" {
		nonce = "nonce"
	}
	return &domain.SSHKeyChallenge{Nonce: nonce, ExpiresIn: 60}, nil
}

func (s *stubSSHExecutor) CompleteLoginSSHKey(_ context.Context, _, _, _ string, _, _ []byte) (*domain.LoginResult, error) {
	i := s.completeCalls
	s.completeCalls++
	var result *domain.LoginResult
	if i < len(s.results) {
		result = s.results[i]
	}
	var err error
	if i < len(s.completeErrs) {
		err = s.completeErrs[i]
	}
	return result, err
}

type stubFullExecutor struct {
	*stubLoginExecutor
	*stubSSHExecutor
}

type failingSigner struct{}

func (failingSigner) PublicKey() ssh.PublicKey { return nil }

func (failingSigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	return nil, errors.New("signing failed")
}

func newSSHTestAuth(sshExec *stubSSHExecutor, storage secureStorage) *Auth {
	return NewAuth(&stubFullExecutor{stubLoginExecutor: &stubLoginExecutor{}, stubSSHExecutor: sshExec}, storage, nil)
}

func TestAuth_LoginWithSSHKey_Success(t *testing.T) {
	executor := &stubSSHExecutor{results: []*domain.LoginResult{{AccessToken: "tok", RefreshToken: "refresh", Host: "h"}}}
	storage := &stubSecureStorage{}
	auth := newSSHTestAuth(executor, storage)

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", testSigners(t, "key"))
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", executor.lastEmail)
	require.Len(t, storage.savedTokens, 1)
	require.Equal(t, "tok", storage.savedTokens[0].AccessToken)
	require.Equal(t, "alice@example.com", storage.savedTokens[0].Email)
}

func TestAuth_LoginWithSSHKey_RetriesNextSigner(t *testing.T) {
	executor := &stubSSHExecutor{
		results:      []*domain.LoginResult{nil, {AccessToken: "tok", Host: "h"}},
		completeErrs: []error{domain.NewUserError("invalid email or SSH key"), nil},
	}
	storage := &stubSecureStorage{}
	auth := newSSHTestAuth(executor, storage)

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", testSigners(t, "first", "second"))
	require.NoError(t, err)
	require.Equal(t, 2, executor.completeCalls)
	require.Equal(t, "tok", storage.savedTokens[0].AccessToken)
}

func TestAuth_LoginWithSSHKey_StopsOnServerError(t *testing.T) {
	wantErr := &domain.Error{Code: 500, Message: "database error"}
	executor := &stubSSHExecutor{
		results:      []*domain.LoginResult{nil, {AccessToken: "tok"}},
		completeErrs: []error{wantErr, nil},
	}
	auth := newSSHTestAuth(executor, &stubSecureStorage{})

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", testSigners(t, "first", "second"))
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, executor.completeCalls)
}

func TestAuth_LoginWithSSHKey_BeginError(t *testing.T) {
	wantErr := errors.New("connection refused")
	executor := &stubSSHExecutor{beginErr: wantErr}
	auth := newSSHTestAuth(executor, &stubSecureStorage{})

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", testSigners(t, "key"))
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_LoginWithSSHKey_KeyOnly(t *testing.T) {
	executor := &stubSSHExecutor{results: []*domain.LoginResult{{AccessToken: "tok", RefreshToken: "refresh", Host: "h"}}}
	storage := &stubSecureStorage{}
	auth := newSSHTestAuth(executor, storage)

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "", testSigners(t, "key"))
	require.NoError(t, err)
	require.Empty(t, executor.lastEmail)
	require.Len(t, storage.savedTokens, 1)
	require.Equal(t, "tok", storage.savedTokens[0].AccessToken)
	require.Empty(t, storage.savedTokens[0].Email)
}

func TestAuth_LoginWithSSHKey_NoSigners(t *testing.T) {
	auth := newSSHTestAuth(&stubSSHExecutor{}, &stubSecureStorage{})

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", nil)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestAuth_LoginWithSSHKey_ExecutorWithoutSSHSupport(t *testing.T) {
	auth := NewAuth(&stubLoginExecutor{}, &stubSecureStorage{}, nil)

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", testSigners(t, "key"))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestAuth_MakeSureLoggedIn_SSHFirst(t *testing.T) {
	executor := &stubFullExecutor{
		stubLoginExecutor: &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "password-tok", Host: "h"}},
		stubSSHExecutor:   &stubSSHExecutor{results: []*domain.LoginResult{{AccessToken: "ssh-tok", Host: "h"}}},
	}
	storage := &stubSecureStorage{loadErr: errors.New("no token")}
	input := &stubUserInput{err: errors.New("should not be prompted")}
	auth := NewAuth(executor, storage, input).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	require.NoError(t, auth.MakeSureLoggedIn(context.Background(), "example.com"))
	require.Empty(t, executor.lastEmail)
	require.Empty(t, executor.lastPassword)
	require.Equal(t, "ssh-tok", storage.savedTokens[0].AccessToken)
	require.Empty(t, storage.savedTokens[0].Email)
}

func TestAuth_MakeSureLoggedIn_SSHFailureFallsBackToPassword(t *testing.T) {
	executor := &stubFullExecutor{
		stubLoginExecutor: &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "password-tok", Host: "h"}},
		stubSSHExecutor: &stubSSHExecutor{
			completeErrs: []error{domain.NewUserError("invalid email or SSH key")},
		},
	}
	storage := &stubSecureStorage{loadErr: errors.New("no token")}
	input := &stubUserInput{username: "alice@example.com", password: "secret"}
	auth := NewAuth(executor, storage, input).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	require.NoError(t, auth.MakeSureLoggedIn(context.Background(), "example.com"))
	require.Equal(t, "alice@example.com", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
	require.Equal(t, "password-tok", storage.savedTokens[0].AccessToken)
	require.Equal(t, "alice@example.com", storage.savedTokens[0].Email)
}

func TestAuth_MakeSureLoggedIn_NoSignersUsesPasswordPrompt(t *testing.T) {
	executor := &stubFullExecutor{
		stubLoginExecutor: &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "password-tok", Host: "h"}},
		stubSSHExecutor:   &stubSSHExecutor{},
	}
	storage := &stubSecureStorage{loadErr: errors.New("no token")}
	input := &stubUserInput{username: "alice@example.com", password: "secret"}
	auth := NewAuth(executor, storage, input).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return nil, nil })

	require.NoError(t, auth.MakeSureLoggedIn(context.Background(), "example.com"))
	require.Equal(t, "secret", executor.lastPassword)
	require.Zero(t, executor.completeCalls)
}

type stubSSHSessionExecutor struct {
	refresh *stubRefreshExecutor
	ssh     *stubSSHExecutor
}

func (s *stubSSHSessionExecutor) LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error) {
	return s.refresh.LoginWithRefreshToken(ctx, host, refreshToken)
}

func (s *stubSSHSessionExecutor) LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error) {
	return s.refresh.LoginWithUsernamePassword(ctx, host, username, password)
}

func (s *stubSSHSessionExecutor) BeginLoginSSHKey(ctx context.Context, host, email string) (*domain.SSHKeyChallenge, error) {
	return s.ssh.BeginLoginSSHKey(ctx, host, email)
}

func (s *stubSSHSessionExecutor) CompleteLoginSSHKey(ctx context.Context, host, email, nonce string, publicKey, signature []byte) (*domain.LoginResult, error) {
	return s.ssh.CompleteLoginSSHKey(ctx, host, email, nonce, publicKey, signature)
}

func TestSession_ReauthViaSSH(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{
		RefreshToken: "old-refresh",
		Email:        "alice@example.com",
		Host:         "example.com",
	}}
	executor := &stubSSHSessionExecutor{
		refresh: &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")},
		ssh: &stubSSHExecutor{results: []*domain.LoginResult{{
			AccessToken:  "ssh-access",
			RefreshToken: "ssh-refresh",
			Host:         "example.com",
		}}},
	}
	session := NewSession(storage, executor).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "ssh-access", token)
	require.Equal(t, 1, executor.ssh.completeCalls)
	require.Len(t, storage.savedTokens, 1)
	require.Equal(t, "alice@example.com", storage.savedTokens[0].Email)
}

func TestSession_ReauthViaSSH_KeyOnly(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{
		RefreshToken: "old-refresh",
		Host:         "example.com",
	}}
	executor := &stubSSHSessionExecutor{
		refresh: &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")},
		ssh: &stubSSHExecutor{results: []*domain.LoginResult{{
			AccessToken:  "ssh-access",
			RefreshToken: "ssh-refresh",
			Host:         "example.com",
		}}},
	}
	session := NewSession(storage, executor).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "ssh-access", token)
	require.Equal(t, 1, executor.ssh.completeCalls)
	require.Empty(t, executor.ssh.lastEmail)
	require.Len(t, storage.savedTokens, 1)
	require.Empty(t, storage.savedTokens[0].Email)
}

func TestSession_Refresh_PreservesEmail(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{
		RefreshToken: "old-refresh",
		Email:        "alice@example.com",
		Host:         "example.com",
	}}
	executor := &stubRefreshExecutor{result: &domain.LoginResult{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		Host:         "example.com",
	}}
	session := NewSession(storage, executor)

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "new-access", token)
	require.Equal(t, "alice@example.com", storage.savedTokens[0].Email)
}

func TestAuth_LoginWithSSHKey_SignerFailureTriesNext(t *testing.T) {
	executor := &stubSSHExecutor{results: []*domain.LoginResult{{AccessToken: "tok", Host: "h"}}}
	storage := &stubSecureStorage{}
	auth := newSSHTestAuth(executor, storage)

	broken := sshkey.Signer{Signer: failingSigner{}, Name: "broken"}
	signers := append([]sshkey.Signer{broken}, testSSHSigner(t, "good"))

	err := auth.LoginWithSSHKey(context.Background(), "example.com", "alice@example.com", signers)
	require.NoError(t, err)
	require.Equal(t, 1, executor.completeCalls)
	require.Equal(t, "tok", storage.savedTokens[0].AccessToken)
}

func TestLoginWithSSHKey_NoSignersAvailable(t *testing.T) {
	_, err := loginWithSSHKey(context.Background(), &stubSSHExecutor{}, "example.com", "alice@example.com", nil)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "no SSH key available for login", domErr.Message)
}

func TestAuth_IsLoggedIn_InvalidTokens(t *testing.T) {
	cases := map[string]string{
		"not a jwt":  "invalid",
		"bad base64": "a.!!!.c",
		"bad claims": "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c",
	}
	for name, token := range cases {
		storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
		auth := NewAuth(&stubLoginExecutor{}, storage, nil)

		loggedIn, err := auth.isLoggedIn("example.com")
		require.False(t, loggedIn, name)
		require.Error(t, err, name)
	}
}

func TestAuth_MakeSureLoggedIn_SSHFailurePromptError(t *testing.T) {
	executor := &stubFullExecutor{
		stubLoginExecutor: &stubLoginExecutor{},
		stubSSHExecutor: &stubSSHExecutor{
			completeErrs: []error{domain.NewUserError("invalid email or SSH key")},
		},
	}
	storage := &stubSecureStorage{loadErr: errors.New("no token")}
	input := &stubUserInput{err: errors.New("stdin closed")}
	auth := NewAuth(executor, storage, input).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.ErrorContains(t, err, "stdin closed")
}

func TestSession_Refresh_LoadError(t *testing.T) {
	storage := &stubSecureStorage{loadErr: errors.New("no token")}
	session := NewSession(storage, &stubRefreshExecutor{})

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorContains(t, err, "no token")
}

func TestSession_Reauth_EmptyUsername(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "rt", Host: "example.com"}}
	executor := &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")}
	session := NewSession(storage, executor, &stubUserInput{username: "", password: "secret"})

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorContains(t, err, "username cannot be empty")
}

func TestSession_Reauth_SSHNotSupported(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{
		RefreshToken: "rt",
		Email:        "alice@example.com",
		Host:         "example.com",
	}}
	executor := &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")}
	session := NewSession(storage, executor).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorContains(t, err, "refresh token expired")
}

func TestSession_Reauth_ResolverError(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{
		RefreshToken: "rt",
		Email:        "alice@example.com",
		Host:         "example.com",
	}}
	executor := &stubSSHSessionExecutor{
		refresh: &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")},
		ssh:     &stubSSHExecutor{},
	}
	session := NewSession(storage, executor).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return nil, errors.New("config broken") })

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorContains(t, err, "refresh token expired")
	require.Zero(t, executor.ssh.completeCalls)
}

func TestSession_Reauth_SSHFailureFallsBackToCause(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{
		RefreshToken: "rt",
		Email:        "alice@example.com",
		Host:         "example.com",
	}}
	executor := &stubSSHSessionExecutor{
		refresh: &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")},
		ssh: &stubSSHExecutor{
			completeErrs: []error{domain.NewUserError("invalid email or SSH key")},
		},
	}
	session := NewSession(storage, executor).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorContains(t, err, "refresh token expired")
	require.Equal(t, 1, executor.ssh.completeCalls)
}

func TestSession_Reauth_SaveError(t *testing.T) {
	storage := &stubSecureStorage{
		saveErr: errors.New("keyring locked"),
		loadResult: &domain.LoginResult{
			RefreshToken: "rt",
			Email:        "alice@example.com",
			Host:         "example.com",
		},
	}
	executor := &stubSSHSessionExecutor{
		refresh: &stubRefreshExecutor{err: domain.NewUserError("refresh token expired")},
		ssh:     &stubSSHExecutor{results: []*domain.LoginResult{{AccessToken: "ssh-access", Host: "example.com"}}},
	}
	session := NewSession(storage, executor).
		WithSSHLogin(func() ([]sshkey.Signer, error) { return testSigners(t, "key"), nil })

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorContains(t, err, "refresh token expired")
}
