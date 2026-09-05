package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubRefreshExecutor struct {
	result      *domain.LoginResult
	err         error
	lastRefresh string
}

func (s *stubRefreshExecutor) LoginWithRefreshToken(_ context.Context, _, refreshToken string) (*domain.LoginResult, error) {
	s.lastRefresh = refreshToken
	return s.result, s.err
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