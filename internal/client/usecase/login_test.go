package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubLoginExecutor struct {
	usernameResult *domain.LoginResult
	usernameErr    error
	lastUsername   string
	lastPassword   string
}

func (s *stubLoginExecutor) LoginWithUsernamePassword(_ context.Context, _, username, password string) (*domain.LoginResult, error) {
	s.lastUsername = username
	s.lastPassword = password
	return s.usernameResult, s.usernameErr
}

type stubSecureStorage struct {
	saveErr     error
	loadResult  *domain.LoginResult
	loadErr     error
	savedTokens []*domain.LoginResult
}

func (s *stubSecureStorage) SaveToken(data *domain.LoginResult) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.savedTokens = append(s.savedTokens, data)
	return nil
}

func (s *stubSecureStorage) LoadToken(_ string) (*domain.LoginResult, error) {
	return s.loadResult, s.loadErr
}

type stubUserInput struct {
	username string
	password string
	err      error
}

func (s *stubUserInput) PromptUsernameAndPassword() (string, string, error) {
	return s.username, s.password, s.err
}

func signTestToken(t *testing.T, secret string) string {
	t.Helper()
	claims := serverDomain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: snow.ID(42),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return signed
}

func TestAuth_LoginWithUsernamePassword_Success(t *testing.T) {
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "tok", Host: "h"}}
	storage := &stubSecureStorage{}
	auth := NewAuth(executor, storage, nil)

	err := auth.LoginWithUsernamePassword(context.Background(), "example.com", "apin", "secret")
	require.NoError(t, err)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
	require.Len(t, storage.savedTokens, 1)
}

func TestAuth_LoginWithUsernamePassword_EmptyUsername(t *testing.T) {
	auth := NewAuth(&stubLoginExecutor{}, &stubSecureStorage{}, nil)

	err := auth.LoginWithUsernamePassword(context.Background(), "example.com", "", "secret")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "username cannot be empty", domErr.Message)
}

func TestAuth_LoginWithUsernamePassword_ExecutorError(t *testing.T) {
	wantErr := errors.New("login failed")
	executor := &stubLoginExecutor{usernameErr: wantErr}
	storage := &stubSecureStorage{}
	auth := NewAuth(executor, storage, nil)

	err := auth.LoginWithUsernamePassword(context.Background(), "example.com", "apin", "secret")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, storage.savedTokens)
}

func TestAuth_LoginWithUsernamePassword_SaveError(t *testing.T) {
	wantErr := errors.New("save failed")
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "tok"}}
	storage := &stubSecureStorage{saveErr: wantErr}
	auth := NewAuth(executor, storage, nil)

	err := auth.LoginWithUsernamePassword(context.Background(), "example.com", "apin", "secret")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_MakeSureLoggedIn_AlreadyLoggedIn(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token, Host: "example.com"}}
	input := &stubUserInput{username: "never", password: "called"}
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "tok"}}
	auth := NewAuth(executor, storage, input)

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.NoError(t, err)
	require.Empty(t, storage.savedTokens)
}

func TestAuth_MakeSureLoggedIn_NotLoggedIn(t *testing.T) {
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "tok"}}
	auth := NewAuth(executor, storage, input)

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
	require.Len(t, storage.savedTokens, 1)
}

func TestAuth_MakeSureLoggedIn_PromptError(t *testing.T) {
	wantErr := errors.New("prompt interrupted")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{err: wantErr}
	executor := &stubLoginExecutor{}
	auth := NewAuth(executor, storage, input)

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, storage.savedTokens)
}

func TestAuth_MakeSureLoggedIn_LoginError(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)

	err := auth.MakeSureLoggedIn(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, storage.savedTokens)
}

func TestAuth_IsLoggedIn_InvalidToken(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "not-a-jwt", Host: "example.com"}}
	auth := NewAuth(nil, storage, nil)

	loggedIn, err := auth.isLoggedIn("example.com")
	require.False(t, loggedIn)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 401, domErr.Code)
}
