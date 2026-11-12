package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubRefreshExecutor struct {
	result         *domain.LoginResult
	err            error
	lastRefresh    string
	passwordResult *domain.LoginResult
	passwordErr    error
	lastUsername   string
	lastPassword   string
}

func (s *stubRefreshExecutor) LoginWithRefreshToken(_ context.Context, _, refreshToken string) (*domain.LoginResult, error) {
	s.lastRefresh = refreshToken
	return s.result, s.err
}

func (s *stubRefreshExecutor) LoginWithUsernamePassword(_ context.Context, _, username, password string) (*domain.LoginResult, error) {
	s.lastUsername = username
	s.lastPassword = password
	return s.passwordResult, s.passwordErr
}

func TestSession_AccessToken_Success(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "access-token", Host: "example.com"}}
	session := NewSession(storage, &stubRefreshExecutor{})

	token, err := session.AccessToken(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "access-token", token)
}

func TestSession_AccessToken_Error(t *testing.T) {
	wantErr := errors.New("load failed")
	storage := &stubSecureStorage{loadErr: wantErr}
	session := NewSession(storage, &stubRefreshExecutor{})

	_, err := session.AccessToken(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestSession_Refresh_Success(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "old-refresh", Host: "example.com"}}
	executor := &stubRefreshExecutor{result: &domain.LoginResult{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		Host:         "example.com",
	}}
	session := NewSession(storage, executor)

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "new-access", token)
	require.Equal(t, "old-refresh", executor.lastRefresh)
	require.Len(t, storage.savedTokens, 1)
	require.Equal(t, "new-access", storage.savedTokens[0].AccessToken)
}

func TestSession_Refresh_NoRefreshToken(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "tok", Host: "example.com"}}
	session := NewSession(storage, &stubRefreshExecutor{})

	_, err := session.Refresh(context.Background(), "example.com")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "no refresh token stored", domErr.Message)
}

func TestSession_Refresh_ExecutorError(t *testing.T) {
	wantErr := errors.New("refresh failed")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "old-refresh", Host: "example.com"}}
	executor := &stubRefreshExecutor{err: wantErr}
	session := NewSession(storage, executor)

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, storage.savedTokens)
}

func TestSession_Refresh_SaveError(t *testing.T) {
	wantErr := errors.New("save failed")
	storage := &stubSecureStorage{
		saveErr:    wantErr,
		loadResult: &domain.LoginResult{RefreshToken: "old-refresh", Host: "example.com"},
	}
	executor := &stubRefreshExecutor{result: &domain.LoginResult{AccessToken: "new-access", Host: "example.com"}}
	session := NewSession(storage, executor)

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestSession_Refresh_RejectedFallsBackToPasswordLogin(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "revoked", Host: "example.com"}}
	executor := &stubRefreshExecutor{
		err:            domain.NewTokenError("refresh token expired"),
		passwordResult: &domain.LoginResult{AccessToken: "password-access", RefreshToken: "new-refresh", Host: "example.com"},
	}
	session := NewSession(storage, executor, &stubUserInput{username: "apin", password: "secret"})

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "password-access", token, "a rejected refresh token must trigger a fresh username/password login")
	require.Equal(t, "revoked", executor.lastRefresh)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
	require.Len(t, storage.savedTokens, 1)
	require.Equal(t, "password-access", storage.savedTokens[0].AccessToken)
}

func TestSession_Refresh_RejectedNoPrompt(t *testing.T) {
	wantErr := domain.NewTokenError("refresh token not found")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "revoked", Host: "example.com"}}
	executor := &stubRefreshExecutor{err: wantErr}
	session := NewSession(storage, executor)

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorAs(t, err, &wantErr)
	require.Empty(t, storage.savedTokens)
}

func TestSession_Refresh_RejectedPromptFails(t *testing.T) {
	wantErr := errors.New("prompt interrupted")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "revoked", Host: "example.com"}}
	executor := &stubRefreshExecutor{err: domain.NewTokenError("refresh token expired")}
	session := NewSession(storage, executor, &stubUserInput{err: wantErr})

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestSession_Refresh_RejectedLoginFails(t *testing.T) {
	wantErr := errors.New("invalid credentials")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{RefreshToken: "revoked", Host: "example.com"}}
	executor := &stubRefreshExecutor{
		err:         domain.NewTokenError("refresh token expired"),
		passwordErr: wantErr,
	}
	session := NewSession(storage, executor, &stubUserInput{username: "apin", password: "secret"})

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, storage.savedTokens)
}

func TestSession_Refresh_RejectedSaveFails(t *testing.T) {
	wantErr := errors.New("save failed")
	storage := &stubSecureStorage{
		saveErr:    wantErr,
		loadResult: &domain.LoginResult{RefreshToken: "revoked", Host: "example.com"},
	}
	executor := &stubRefreshExecutor{
		err:            domain.NewTokenError("refresh token expired"),
		passwordResult: &domain.LoginResult{AccessToken: "password-access", Host: "example.com"},
	}
	session := NewSession(storage, executor, &stubUserInput{username: "apin", password: "secret"})

	_, err := session.Refresh(context.Background(), "example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestSession_Refresh_NoRefreshTokenFallsBackToPasswordLogin(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "access", Host: "example.com"}}
	executor := &stubRefreshExecutor{
		passwordResult: &domain.LoginResult{AccessToken: "password-access", RefreshToken: "new-refresh", Host: "example.com"},
	}
	session := NewSession(storage, executor, &stubUserInput{username: "apin", password: "secret"})

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "password-access", token)
	require.Equal(t, "apin", executor.lastUsername)
}

type sequenceStorage struct {
	loads []*domain.LoginResult
	calls int
	err   error
	saved []*domain.LoginResult
}

func (s *sequenceStorage) SaveToken(data *domain.LoginResult) error {
	s.saved = append(s.saved, data)
	return nil
}

func (s *sequenceStorage) LoadToken(string) (*domain.LoginResult, error) {
	i := min(s.calls, len(s.loads)-1)
	s.calls++
	if s.err != nil && s.calls > 1 {
		return nil, s.err
	}
	return s.loads[i], nil
}

func noWait(context.Context, time.Duration) error { return nil }

func TestSession_Refresh_UsesTokensRotatedByAnotherProcess(t *testing.T) {
	storage := &sequenceStorage{loads: []*domain.LoginResult{
		{AccessToken: "old-access", RefreshToken: "1a.tag.old-secret", Host: "example.com"},
		{AccessToken: "other-access", RefreshToken: "1a.tag.new-secret", Host: "example.com"},
	}}
	executor := &stubRefreshExecutor{err: &domain.Error{Code: 404, Message: "record not found"}}
	session := NewSession(storage, executor, &stubUserInput{username: "u", password: "p"})
	session.wait = noWait

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "other-access", token)
	require.Equal(t, "1a.tag.old-secret", executor.lastRefresh)
	require.Empty(t, executor.lastUsername, "no prompt when another process rotated")
	require.Empty(t, storage.saved)
}

func TestSession_Refresh_WaitsForAConcurrentSave(t *testing.T) {
	old := &domain.LoginResult{AccessToken: "old-access", RefreshToken: "old-refresh", Email: "alice@example.com", Host: "example.com"}
	storage := &sequenceStorage{loads: []*domain.LoginResult{old, old, old,
		{AccessToken: "other-access", RefreshToken: "1a.tag.secret", Email: "alice@example.com", Host: "example.com"}}}
	executor := &stubRefreshExecutor{err: &domain.Error{Code: 404, Message: "record not found"}}
	session := NewSession(storage, executor)
	waits := 0
	session.wait = func(context.Context, time.Duration) error { waits++; return nil }

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "other-access", token)
	require.Equal(t, 2, waits)
}

func TestSession_Refresh_RevokedSessionFallsBackToReauth(t *testing.T) {
	stored := &domain.LoginResult{AccessToken: "old-access", RefreshToken: "old-refresh", Host: "example.com"}
	storage := &sequenceStorage{loads: []*domain.LoginResult{stored}}
	executor := &stubRefreshExecutor{
		err:            &domain.Error{Code: 401, Message: "refresh token revoked"},
		passwordResult: &domain.LoginResult{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", Host: "example.com"},
	}
	session := NewSession(storage, executor, &stubUserInput{username: "alice@example.com", password: "pw"})
	waits := 0
	session.wait = func(context.Context, time.Duration) error { waits++; return nil }

	token, err := session.Refresh(context.Background(), "example.com")
	require.NoError(t, err)
	require.Equal(t, "fresh-access", token)
	require.Equal(t, 2, storage.calls, "a 401 reloads the store once")
	require.Zero(t, waits)
	require.Equal(t, "alice@example.com", executor.lastUsername)
}

func TestSession_Refresh_NotFoundGivesUpAfterPolling(t *testing.T) {
	stored := &domain.LoginResult{AccessToken: "old-access", RefreshToken: "old-refresh", Host: "example.com"}
	storage := &sequenceStorage{loads: []*domain.LoginResult{stored}}
	executor := &stubRefreshExecutor{err: &domain.Error{Code: 404, Message: "record not found"}}
	session := NewSession(storage, executor)
	session.wait = noWait

	_, err := session.Refresh(context.Background(), "example.com")
	require.Error(t, err)
	require.Equal(t, 1+refreshReloadAttempts, storage.calls)
}

func TestSession_Refresh_ReloadStopsOnCancelAndLoadError(t *testing.T) {
	stored := &domain.LoginResult{AccessToken: "old-access", RefreshToken: "old-refresh", Host: "example.com"}
	executor := &stubRefreshExecutor{err: &domain.Error{Code: 404, Message: "record not found"}}

	storage := &sequenceStorage{loads: []*domain.LoginResult{stored}}
	session := NewSession(storage, executor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := session.Refresh(ctx, "example.com")
	require.Error(t, err)
	require.Equal(t, 2, storage.calls)

	storage = &sequenceStorage{loads: []*domain.LoginResult{stored}, err: errors.New("keyring locked")}
	session = NewSession(storage, executor)
	session.wait = noWait
	_, err = session.Refresh(context.Background(), "example.com")
	require.Error(t, err)
	require.Equal(t, 2, storage.calls)
}

func TestWaitContext(t *testing.T) {
	require.NoError(t, waitContext(context.Background(), time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitContext(ctx, time.Hour), context.Canceled)
}

func TestSession_Refresh_DoesNotAdoptAnotherAccount(t *testing.T) {
	for name, stored := range map[string]*domain.LoginResult{
		"other session":         {AccessToken: "bob-access", RefreshToken: "2b.tag.secret", Email: "bob@example.com"},
		"other email":           {AccessToken: "bob-access", RefreshToken: "bob-legacy", Email: "bob@example.com"},
		"legacy without email":  {AccessToken: "bob-access", RefreshToken: "bob-legacy"},
		"same session, no mail": {AccessToken: "", RefreshToken: "1a.tag.new-secret"},
	} {
		t.Run(name, func(t *testing.T) {
			presented := &domain.LoginResult{AccessToken: "old-access", RefreshToken: "1a.tag.old-secret", Email: "alice@example.com", Host: "example.com"}
			storage := &sequenceStorage{loads: []*domain.LoginResult{presented, stored}}
			executor := &stubRefreshExecutor{
				err:            &domain.Error{Code: 401, Message: "refresh token revoked"},
				passwordResult: &domain.LoginResult{AccessToken: "fresh-access", RefreshToken: "fresh-refresh"},
			}
			session := NewSession(storage, executor, &stubUserInput{username: "alice@example.com", password: "pw"})
			session.wait = noWait

			token, err := session.Refresh(context.Background(), "example.com")
			require.NoError(t, err)
			require.Equal(t, "fresh-access", token, "a different account in the shared store is never adopted")
		})
	}
}

func TestSameSession(t *testing.T) {
	require.True(t, sameSession(&domain.LoginResult{RefreshToken: "1a.t.x"}, &domain.LoginResult{RefreshToken: "1a.t.y"}))
	require.False(t, sameSession(&domain.LoginResult{RefreshToken: "1a.t.x"}, &domain.LoginResult{RefreshToken: "1a.u.y"}))
	require.True(t, sameSession(&domain.LoginResult{RefreshToken: "legacy", Email: "a@x"}, &domain.LoginResult{RefreshToken: "2b.t.y", Email: "a@x"}))
	require.False(t, sameSession(&domain.LoginResult{RefreshToken: "legacy"}, &domain.LoginResult{RefreshToken: "legacy2"}))
	require.Empty(t, refreshSessionID(".t.x"))
	require.Empty(t, refreshSessionID("a..x"))
}
