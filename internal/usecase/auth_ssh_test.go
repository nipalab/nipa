package usecase

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/sshkey"
)

func sshLoginTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return signer
}

func sshLoginTestSignature(t *testing.T, signer ssh.Signer, payload []byte) []byte {
	t.Helper()
	sig, err := signer.Sign(rand.Reader, payload)
	require.NoError(t, err)
	return ssh.Marshal(sig)
}

func TestAuth_BeginSSHKeyLogin_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo)

	authRepo.EXPECT().
		DeleteExpiredSSHLoginChallenges(gomock.Any(), gomock.Any()).
		Return(nil)

	var capturedEmail, capturedNonce string
	var capturedExpiry time.Time
	authRepo.EXPECT().
		CreateSSHLoginChallenge(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, email, nonce string, expiresAt time.Time) error {
			capturedEmail, capturedNonce, capturedExpiry = email, nonce, expiresAt
			return nil
		})

	challenge, err := auth.BeginSSHKeyLogin(context.Background(), "  alice@example.com ")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", capturedEmail)
	require.NotEmpty(t, capturedNonce)
	require.Equal(t, capturedNonce, challenge.Nonce)
	require.Equal(t, 60, challenge.ExpiresIn)
	require.WithinDuration(t, time.Now().Add(60*time.Second), capturedExpiry, 5*time.Second)
}

func TestAuth_BeginSSHKeyLogin_EmptyEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl))

	_, err := auth.BeginSSHKeyLogin(context.Background(), "   ")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestAuth_BeginSSHKeyLogin_SweepError(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), authRepo)

	wantErr := errors.New("db down")
	authRepo.EXPECT().
		DeleteExpiredSSHLoginChallenges(gomock.Any(), gomock.Any()).
		Return(wantErr)

	_, err := auth.BeginSSHKeyLogin(context.Background(), "alice@example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_BeginSSHKeyLogin_CreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), authRepo)

	wantErr := errors.New("db down")
	authRepo.EXPECT().
		DeleteExpiredSSHLoginChallenges(gomock.Any(), gomock.Any()).
		Return(nil)
	authRepo.EXPECT().
		CreateSSHLoginChallenge(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(wantErr)

	_, err := auth.BeginSSHKeyLogin(context.Background(), "alice@example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_CompleteSSHKeyLogin_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)
	keyRepo := NewMockauthSSHKeyRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).WithSSHKeys(keyRepo)

	signer := sshLoginTestSigner(t)
	nonce := "nonce-1"
	user := &domain.User{ID: snow.ID(42), Email: "alice@example.com", IsAdmin: true}

	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), nonce).
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     nonce,
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(user, nil)
	keyRepo.EXPECT().
		GetByFingerprint(gomock.Any(), ssh.FingerprintSHA256(signer.PublicKey())).
		Return(&domain.SSHPublicKey{ID: snow.ID(1), UserID: snow.ID(42)}, nil)
	authRepo.EXPECT().
		SaveRefreshToken(gomock.Any(), snow.ID(42), gomock.Any(), gomock.Any()).
		Return(nil)

	result, err := auth.CompleteSSHKeyLogin(
		context.Background(),
		"alice@example.com",
		nonce,
		signer.PublicKey().Marshal(),
		sshLoginTestSignature(t, signer, sshkey.LoginPayload(nonce)),
	)
	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	require.NotEmpty(t, result.RefreshToken)
	require.Equal(t, "Bearer", result.TokenType)

	claims := parseAccessToken(t, result.AccessToken, "test-secret")
	require.Equal(t, snow.ID(42), claims.UserID)
}

func TestAuth_CompleteSSHKeyLogin_EmptyInputs(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl)).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	ctx := context.Background()
	pub := []byte("key")
	sig := []byte("sig")

	for name, err := range map[string]error{
		"email":      mustComplete(t, auth, ctx, "", "nonce", pub, sig),
		"nonce":      mustComplete(t, auth, ctx, "a@b.c", "", pub, sig),
		"public key": mustComplete(t, auth, ctx, "a@b.c", "nonce", nil, sig),
		"signature":  mustComplete(t, auth, ctx, "a@b.c", "nonce", pub, nil),
	} {
		var domErr *domain.Error
		require.ErrorAs(t, err, &domErr, name)
		require.Equal(t, 400, domErr.Code, name)
	}
}

func mustComplete(t *testing.T, auth *Auth, ctx context.Context, email, nonce string, publicKey, signature []byte) error {
	t.Helper()
	_, err := auth.CompleteSSHKeyLogin(ctx, email, nonce, publicKey, signature)
	return err
}

func TestAuth_CompleteSSHKeyLogin_NoKeyRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl))

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_UnknownNonce(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_Expired(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(-time.Second),
		}, nil)

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_EmailMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "bob@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_UnknownUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	userRepo := NewMockuserRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_InvalidPublicKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	userRepo := NewMockuserRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(&domain.User{ID: snow.ID(42), Email: "alice@example.com"}, nil)

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("junk"), []byte("sig"))
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_KeyNotRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	userRepo := NewMockuserRepository(ctrl)
	keyRepo := NewMockauthSSHKeyRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).WithSSHKeys(keyRepo)

	signer := sshLoginTestSigner(t)
	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(&domain.User{ID: snow.ID(42), Email: "alice@example.com"}, nil)
	keyRepo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := auth.CompleteSSHKeyLogin(
		context.Background(), "alice@example.com", "nonce",
		signer.PublicKey().Marshal(), sshLoginTestSignature(t, signer, sshkey.LoginPayload("nonce")),
	)
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_KeyBelongsToOtherUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	userRepo := NewMockuserRepository(ctrl)
	keyRepo := NewMockauthSSHKeyRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).WithSSHKeys(keyRepo)

	signer := sshLoginTestSigner(t)
	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(&domain.User{ID: snow.ID(42), Email: "alice@example.com"}, nil)
	keyRepo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(&domain.SSHPublicKey{ID: snow.ID(1), UserID: snow.ID(99)}, nil)

	_, err := auth.CompleteSSHKeyLogin(
		context.Background(), "alice@example.com", "nonce",
		signer.PublicKey().Marshal(), sshLoginTestSignature(t, signer, sshkey.LoginPayload("nonce")),
	)
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_BadSignature(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	userRepo := NewMockuserRepository(ctrl)
	keyRepo := NewMockauthSSHKeyRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).WithSSHKeys(keyRepo)

	signer := sshLoginTestSigner(t)
	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(&domain.User{ID: snow.ID(42), Email: "alice@example.com"}, nil)
	keyRepo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(&domain.SSHPublicKey{ID: snow.ID(1), UserID: snow.ID(42)}, nil)

	_, err := auth.CompleteSSHKeyLogin(
		context.Background(), "alice@example.com", "nonce",
		signer.PublicKey().Marshal(), sshLoginTestSignature(t, signer, sshkey.LoginPayload("tampered")),
	)
	assertSSHLoginFailed(t, err)
}

func TestAuth_CompleteSSHKeyLogin_ConsumeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	auth := newTestAuth("test-secret", true, NewMockuserRepository(ctrl), authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	wantErr := errors.New("db down")
	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(nil, wantErr)

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_CompleteSSHKeyLogin_UserLookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	userRepo := NewMockuserRepository(ctrl)
	auth := newTestAuth("test-secret", true, userRepo, authRepo).
		WithSSHKeys(NewMockauthSSHKeyRepository(ctrl))

	wantErr := errors.New("db down")
	authRepo.EXPECT().
		ConsumeSSHLoginChallenge(gomock.Any(), "nonce").
		Return(&domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce",
			ExpiresAt: time.Now().Add(30 * time.Second),
		}, nil)
	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "alice@example.com").
		Return(nil, wantErr)

	_, err := auth.CompleteSSHKeyLogin(context.Background(), "alice@example.com", "nonce", []byte("key"), []byte("sig"))
	require.ErrorIs(t, err, wantErr)
}

func assertSSHLoginFailed(t *testing.T, err error) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "invalid email or SSH key", domErr.Message)
}
