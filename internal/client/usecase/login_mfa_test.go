package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubMFAExecutor struct {
	passwordResult *domain.LoginResult
	passwordErr    error
	mfaErrs        []error
	codes          []string
	challenges     []string
	refreshErr     error
}

func (s *stubMFAExecutor) LoginWithUsernamePassword(context.Context, string, string, string) (*domain.LoginResult, error) {
	return s.passwordResult, s.passwordErr
}

func (s *stubMFAExecutor) CompleteLoginMFA(_ context.Context, host, challenge, code string) (*domain.LoginResult, error) {
	s.codes = append(s.codes, code)
	s.challenges = append(s.challenges, challenge)
	if len(s.mfaErrs) > 0 {
		err := s.mfaErrs[0]
		s.mfaErrs = s.mfaErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return &domain.LoginResult{AccessToken: "mfa-tok", RefreshToken: "mfa-refresh", Host: host}, nil
}

func (s *stubMFAExecutor) LoginWithRefreshToken(context.Context, string, string) (*domain.LoginResult, error) {
	return nil, s.refreshErr
}

type stubOTPInput struct {
	stubUserInput
	codes    []string
	err      error
	attempts []int
}

func (s *stubOTPInput) PromptOTP(attempt int) (string, error) {
	s.attempts = append(s.attempts, attempt)
	if s.err != nil {
		return "", s.err
	}
	code := s.codes[0]
	s.codes = s.codes[1:]
	return code, nil
}

func challengeExecutor() *stubMFAExecutor {
	return &stubMFAExecutor{passwordResult: &domain.LoginResult{MFAChallenge: "ch-1", ExpiresIn: 300}}
}

func noEnv(string) string { return "" }

func newMFAAuth(executor loginExecutor, storage *stubSecureStorage, input userInput, env func(string) string) *Auth {
	auth := NewAuth(executor, storage, input)
	auth.getenv = env
	return auth
}

func TestAuth_LoginWithPassword_OTPFlag(t *testing.T) {
	executor := challengeExecutor()
	storage := &stubSecureStorage{}
	auth := newMFAAuth(executor, storage, &stubUserInput{}, func(string) string { return "999999" })

	require.NoError(t, auth.LoginWithPassword(context.Background(), "https://nipa.example.com", "alice", "pw", " 123456 ", nil))
	require.Equal(t, []string{"123456"}, executor.codes)
	require.Equal(t, []string{"ch-1"}, executor.challenges)
	require.Len(t, storage.savedTokens, 1)
	require.Equal(t, "mfa-tok", storage.savedTokens[0].AccessToken)
	require.Equal(t, "alice", storage.savedTokens[0].Email)
	require.Equal(t, "nipa.example.com", storage.savedTokens[0].Host)
}

func TestAuth_LoginWithPassword_EnvOTP(t *testing.T) {
	executor := challengeExecutor()
	storage := &stubSecureStorage{}
	auth := newMFAAuth(executor, storage, &stubUserInput{}, func(key string) string {
		require.Equal(t, EnvOTP, key)
		return "654321"
	})

	require.NoError(t, auth.LoginWithUsernamePassword(context.Background(), "nipa.example.com", "alice", "pw"))
	require.Equal(t, []string{"654321"}, executor.codes)
	require.Len(t, storage.savedTokens, 1)
}

func TestAuth_LoginWithPassword_NonInteractiveCodeDoesNotRetry(t *testing.T) {
	executor := challengeExecutor()
	executor.mfaErrs = []error{domain.NewUserError("invalid code")}
	storage := &stubSecureStorage{}
	input := &stubOTPInput{codes: []string{"111111"}}
	auth := newMFAAuth(executor, storage, input, noEnv)

	err := auth.LoginWithPassword(context.Background(), "nipa.example.com", "alice", "pw", "000000", nil)
	require.ErrorContains(t, err, "invalid code")
	require.Equal(t, []string{"000000"}, executor.codes)
	require.Empty(t, input.attempts)
	require.Empty(t, storage.savedTokens)
}

func TestAuth_LoginWithPassword_PromptRetriesWrongCode(t *testing.T) {
	executor := challengeExecutor()
	executor.mfaErrs = []error{domain.NewUserError("invalid code"), nil}
	storage := &stubSecureStorage{}
	input := &stubOTPInput{codes: []string{"000000", "123456"}}
	auth := newMFAAuth(executor, storage, &stubUserInput{}, noEnv)

	require.NoError(t, auth.LoginWithPassword(context.Background(), "nipa.example.com", "alice", "pw", "", input))
	require.Equal(t, []int{0, 1}, input.attempts)
	require.Equal(t, []string{"000000", "123456"}, executor.codes)
	require.Len(t, storage.savedTokens, 1)
}

func TestAuth_LoginWithPassword_PromptGivesUp(t *testing.T) {
	executor := challengeExecutor()
	invalid := domain.NewUserError("invalid code")
	executor.mfaErrs = []error{invalid, invalid, invalid, nil}
	input := &stubOTPInput{codes: []string{"1", "2", "3", "4"}}
	auth := newMFAAuth(executor, &stubSecureStorage{}, input, noEnv)

	err := auth.LoginWithUsernamePassword(context.Background(), "nipa.example.com", "alice", "pw")
	require.ErrorIs(t, err, invalid)
	require.Equal(t, []int{0, 1, 2}, input.attempts)
}

func TestAuth_LoginWithPassword_PromptStopsOnOtherErrors(t *testing.T) {
	executor := challengeExecutor()
	executor.mfaErrs = []error{errors.New("connection reset")}
	input := &stubOTPInput{codes: []string{"1", "2"}}
	auth := newMFAAuth(executor, &stubSecureStorage{}, input, noEnv)

	err := auth.LoginWithUsernamePassword(context.Background(), "nipa.example.com", "alice", "pw")
	require.EqualError(t, err, "connection reset")
	require.Equal(t, []int{0}, input.attempts)
}

func TestAuth_LoginWithPassword_PromptError(t *testing.T) {
	executor := challengeExecutor()
	input := &stubOTPInput{err: errors.New("no tty")}
	auth := newMFAAuth(executor, &stubSecureStorage{}, input, noEnv)

	err := auth.LoginWithUsernamePassword(context.Background(), "nipa.example.com", "alice", "pw")
	require.EqualError(t, err, "no tty")
	require.Empty(t, executor.codes)
}

func TestAuth_LoginWithPassword_NoCodeSource(t *testing.T) {
	executor := challengeExecutor()
	storage := &stubSecureStorage{}
	auth := newMFAAuth(executor, storage, &stubUserInput{}, noEnv)

	err := auth.LoginWithUsernamePassword(context.Background(), "nipa.example.com", "alice", "pw")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Contains(t, domErr.Message, "two-factor")
	require.Contains(t, domErr.Hint, "SSH key")
	require.Equal(t, "nipa login", domErr.Action)
	require.Empty(t, storage.savedTokens)
}

func TestAuth_LoginWithPassword_NoChallengeUnchanged(t *testing.T) {
	executor := &stubMFAExecutor{passwordResult: &domain.LoginResult{AccessToken: "tok"}}
	storage := &stubSecureStorage{}
	input := &stubOTPInput{}
	auth := newMFAAuth(executor, storage, input, func(string) string { return "123456" })

	require.NoError(t, auth.LoginWithUsernamePassword(context.Background(), "nipa.example.com", "alice", "pw"))
	require.Empty(t, executor.codes)
	require.Empty(t, input.attempts)
	require.Equal(t, "tok", storage.savedTokens[0].AccessToken)
}

func TestAuth_LoginWithPassword_Errors(t *testing.T) {
	auth := newMFAAuth(challengeExecutor(), &stubSecureStorage{}, nil, noEnv)
	require.ErrorContains(t, auth.LoginWithPassword(context.Background(), "h", "", "pw", "", nil), "username cannot be empty")

	failing := &stubMFAExecutor{passwordErr: domain.NewUserError("invalid email or password")}
	auth = newMFAAuth(failing, &stubSecureStorage{}, nil, noEnv)
	require.ErrorContains(t, auth.LoginWithPassword(context.Background(), "h", "alice", "pw", "1", nil), "invalid email or password")
}

func TestAuth_LoginWithPassword_ExecutorWithoutMFA(t *testing.T) {
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{MFAChallenge: "ch"}}
	auth := newMFAAuth(executor, &stubSecureStorage{}, nil, noEnv)
	err := auth.LoginWithPassword(context.Background(), "h", "alice", "pw", "123456", nil)
	require.ErrorContains(t, err, "not supported")

	err = auth.LoginWithPassword(context.Background(), "h", "alice", "pw", "", &stubOTPInput{codes: []string{"1"}})
	require.ErrorContains(t, err, "not supported")
}

func TestAuth_StartPasswordLogin(t *testing.T) {
	executor := challengeExecutor()
	storage := &stubSecureStorage{}
	auth := newMFAAuth(executor, storage, &stubOTPInput{}, func(string) string { return "123456" })

	challenge, err := auth.StartPasswordLogin(context.Background(), "nipa.example.com", "alice", "pw", "")
	require.NoError(t, err)
	require.Equal(t, &domain.MFAChallenge{Challenge: "ch-1", ExpiresIn: 300}, challenge)
	require.Empty(t, executor.codes)
	require.Empty(t, storage.savedTokens)

	challenge, err = auth.StartPasswordLogin(context.Background(), "nipa.example.com", "alice", "pw", "123456")
	require.NoError(t, err)
	require.Nil(t, challenge)
	require.Equal(t, "alice", storage.savedTokens[0].Email)

	plain := &stubMFAExecutor{passwordResult: &domain.LoginResult{AccessToken: "tok"}}
	auth = newMFAAuth(plain, storage, nil, noEnv)
	challenge, err = auth.StartPasswordLogin(context.Background(), "nipa.example.com", "bob", "pw", "")
	require.NoError(t, err)
	require.Nil(t, challenge)
	require.Equal(t, "bob", storage.savedTokens[1].Email)

	_, err = auth.StartPasswordLogin(context.Background(), "nipa.example.com", "", "pw", "")
	require.ErrorContains(t, err, "username cannot be empty")
}

func TestAuth_CompleteLoginMFA(t *testing.T) {
	executor := &stubMFAExecutor{}
	storage := &stubSecureStorage{}
	auth := newMFAAuth(executor, storage, nil, noEnv)

	require.NoError(t, auth.CompleteLoginMFA(context.Background(), "https://nipa.example.com", "alice", "ch-1", "123456"))
	require.Equal(t, "alice", storage.savedTokens[0].Email)
	require.Equal(t, "nipa.example.com", storage.savedTokens[0].Host)

	require.ErrorContains(t, auth.CompleteLoginMFA(context.Background(), "h", "alice", "", "1"), "challenge cannot be empty")
	require.ErrorContains(t, auth.CompleteLoginMFA(context.Background(), "h", "alice", "ch", "  "), "code cannot be empty")

	executor.mfaErrs = []error{domain.NewUserError("invalid code")}
	require.ErrorContains(t, auth.CompleteLoginMFA(context.Background(), "h", "alice", "ch", "1"), "invalid code")
	require.Len(t, storage.savedTokens, 1)
}

func TestSession_ReauthAsksForCode(t *testing.T) {
	executor := challengeExecutor()
	executor.refreshErr = domain.NewTokenError("refresh token revoked")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "r", Email: "alice"}}
	input := &stubOTPInput{stubUserInput: stubUserInput{username: "alice", password: "pw"}, codes: []string{"123456"}}
	session := NewSession(storage, executor, input)
	session.getenv = noEnv

	token, err := session.Refresh(context.Background(), "nipa.example.com")
	require.NoError(t, err)
	require.Equal(t, "mfa-tok", token)
	require.Equal(t, []string{"123456"}, executor.codes)
	require.Equal(t, "alice", storage.savedTokens[len(storage.savedTokens)-1].Email)
}

func TestSession_ReauthWithoutCodeSourceFails(t *testing.T) {
	executor := challengeExecutor()
	executor.refreshErr = domain.NewTokenError("refresh token revoked")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "r"}}
	session := NewSession(storage, executor, &stubUserInput{username: "alice", password: "pw"})
	session.getenv = noEnv

	_, err := session.Refresh(context.Background(), "nipa.example.com")
	require.ErrorContains(t, err, "two-factor")
	require.Empty(t, storage.savedTokens)
}
