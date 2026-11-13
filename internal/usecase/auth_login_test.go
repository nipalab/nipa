package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/sshkey"
)

type recordingHasher struct {
	match    bool
	compared []string
}

func (h *recordingHasher) Hash(password string) (string, error) { return "dummy:" + password, nil }
func (h *recordingHasher) Compare(hash, _ string) bool {
	h.compared = append(h.compared, hash)
	return h.match && hash != ""
}

func ipCtx(ip string) context.Context {
	return domain.ContextWithRequestMeta(context.Background(), domain.RequestMeta{ClientIP: ip})
}

func loginLimiter(idBurst, emailBurst int) *ratelimit.Limiter {
	return ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
		ratelimit.ScopeAuthID:    {Rate: 0.0001, Burst: idBurst},
		ratelimit.ScopeAuthEmail: {Rate: 0.0001, Burst: emailBurst},
	})
}

func requireTooMany(t *testing.T, err error) {
	t.Helper()
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 429, derr.Code)
}

func requireNotTooMany(t *testing.T, err error) {
	t.Helper()
	var derr *domain.Error
	if errors.As(err, &derr) {
		require.NotEqual(t, 429, derr.Code)
	}
}

func TestAuth_LoginEmptyHashSpendsDummyComparison(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	hasher := &recordingHasher{match: true}
	rec := &captureRecorder{}
	auth := NewAuth("secret", hasher, userRepo, NewMockauthRepository(ctrl)).WithAudit(rec)

	userRepo.EXPECT().GetByEmail(gomock.Any(), "oauth@example.com").Return(&domain.User{ID: 9, EmailVerified: true}, nil)
	_, err := auth.LoginWithEmailPassword(context.Background(), "oauth@example.com", "guess")
	require.EqualError(t, err, "invalid email or password")
	require.Equal(t, []string{"dummy:nipa-timing-equalizer"}, hasher.compared)

	entry := rec.only(t)
	require.Equal(t, domain.AuditAuthLoginFailed, entry.Action)
	require.Equal(t, "9", entry.TargetID)
	require.Equal(t, "no_password", entry.Metadata["reason"])
}

func TestAuth_LoginUnknownUserSpendsDummyComparison(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	hasher := &recordingHasher{}
	rec := &captureRecorder{}
	auth := NewAuth("secret", hasher, userRepo, NewMockauthRepository(ctrl)).WithAudit(rec)

	userRepo.EXPECT().GetByEmail(gomock.Any(), "who@example.com").Return(nil, domain.NewErrorNotFound("x"))
	_, err := auth.LoginWithEmailPassword(context.Background(), " Who@Example.com", "guess")
	require.EqualError(t, err, "invalid email or password")
	require.Equal(t, []string{"dummy:nipa-timing-equalizer"}, hasher.compared)

	entry := rec.only(t)
	require.Equal(t, domain.AuditAuthLoginFailed, entry.Action)
	require.Equal(t, domain.AuditActorSystem, entry.ActorType)
	require.Empty(t, entry.TargetType)
	require.Empty(t, entry.TargetID)
	require.Equal(t, map[string]any{"method": LoginMethodPassword, "email": "who@example.com", "reason": "invalid_credentials"}, entry.Metadata)
}

func TestAuth_LoginThrottleKeyedByEmailAndIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")).AnyTimes()
	auth := NewAuth("secret", nil, userRepo, NewMockauthRepository(ctrl)).WithLimiter(loginLimiter(2, 5))

	for range 2 {
		_, err := auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "victim@example.com", "x")
		require.EqualError(t, err, "boom")
	}
	_, err := auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "victim@example.com", "x")
	requireTooMany(t, err)

	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.2"), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "other@example.com", "x")
	require.EqualError(t, err, "boom")
}

func TestAuth_LoginEmailCeilingBoundsDistributedAttempts(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")).Times(3)
	auth := NewAuth("secret", nil, userRepo, NewMockauthRepository(ctrl)).WithLimiter(loginLimiter(1, 3))

	_, err := auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "victim@example.com", "x")
	requireTooMany(t, err)
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.2"), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.3"), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.4"), "victim@example.com", "x")
	requireTooMany(t, err)
}

func TestAuth_LoginThrottleWithoutIPKeysOnEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")).Times(2)
	auth := NewAuth("secret", nil, userRepo, NewMockauthRepository(ctrl)).WithLimiter(loginLimiter(1, 10))

	_, err := auth.LoginWithEmailPassword(context.Background(), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx(""), "VICTIM@example.com", "x")
	requireTooMany(t, err)

	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.9"), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
}

func TestAuth_ThrottledLoginIsNotAudited(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorNotFound("x"))
	rec := &captureRecorder{}
	auth := NewAuth("secret", &recordingHasher{}, userRepo, NewMockauthRepository(ctrl)).WithLimiter(loginLimiter(1, 10)).WithAudit(rec)

	_, err := auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "a@example.com", "x")
	require.Error(t, err)
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "a@example.com", "x")
	requireTooMany(t, err)
	require.Len(t, rec.entries, 1)
}

func TestAuth_SSHLoginThrottleKeyedByEmailAndIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl)).
		WithSSHAudience(testSSHAudience).WithLimiter(loginLimiter(1, 10))

	_, err := auth.CompleteSSHKeyLogin(ipCtx("10.0.0.1"), "a@example.com", "", nil, nil)
	requireNotTooMany(t, err)
	_, err = auth.BeginSSHKeyLogin(ipCtx("10.0.0.1"), "a@example.com")
	requireTooMany(t, err)
	_, err = auth.CompleteSSHKeyLogin(ipCtx("10.0.0.2"), "a@example.com", "", nil, nil)
	requireNotTooMany(t, err)
}

func TestAuth_PasswordLoginAudit(t *testing.T) {
	user := &domain.User{ID: 42, Email: "user@example.com", Password: "hash", EmailVerified: true}

	t.Run("success", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		authRepo := NewMockauthRepository(ctrl)
		rec := &captureRecorder{}
		auth := newTestAuth("secret", true, userRepo, authRepo).WithAudit(rec)
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)
		authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(42))).Return(int64(1), nil)

		_, err := auth.LoginWithEmailPassword(context.Background(), "user@example.com", "pw")
		require.NoError(t, err)
		entry := rec.only(t)
		require.Equal(t, domain.AuditAuthLoginSucceeded, entry.Action)
		require.Equal(t, domain.AuditActorUser, entry.ActorType)
		require.Equal(t, snow.ID(42), *entry.ActorUserID)
		require.Equal(t, domain.AuditTargetUser, entry.TargetType)
		require.Equal(t, snow.ID(42).Base36(), entry.TargetID)
		require.Equal(t, map[string]any{"method": LoginMethodPassword}, entry.Metadata)
		require.Equal(t, 0, rec.txs)
	})

	t.Run("wrong password", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		rec := &captureRecorder{}
		auth := newTestAuth("secret", false, userRepo, NewMockauthRepository(ctrl)).WithAudit(rec)
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)

		_, err := auth.LoginWithEmailPassword(context.Background(), "user@example.com", "secret-guess")
		require.EqualError(t, err, "invalid email or password")
		entry := rec.only(t)
		require.Equal(t, domain.AuditAuthLoginFailed, entry.Action)
		require.Equal(t, domain.AuditActorSystem, entry.ActorType)
		require.Nil(t, entry.ActorUserID)
		require.Equal(t, snow.ID(42).Base36(), entry.TargetID)
		require.Equal(t, "invalid_credentials", entry.Metadata["reason"])
		require.NotContains(t, entry.Metadata, "password")
	})

	t.Run("unverified", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		rec := &captureRecorder{}
		auth := newTestAuth("secret", true, userRepo, NewMockauthRepository(ctrl)).WithAudit(rec)
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(&domain.User{ID: 42, Password: "hash"}, nil)

		_, err := auth.LoginWithEmailPassword(context.Background(), "user@example.com", "pw")
		require.EqualError(t, err, "email not confirmed")
		require.Equal(t, "email_not_verified", rec.only(t).Metadata["reason"])
	})

	t.Run("audit failure does not fail the login", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		authRepo := NewMockauthRepository(ctrl)
		rec := &captureRecorder{err: errAudit}
		auth := newTestAuth("secret", true, userRepo, authRepo).WithAudit(rec)
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)
		authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(42))).Return(int64(1), nil)

		got, err := auth.LoginWithEmailPassword(context.Background(), "user@example.com", "pw")
		require.NoError(t, err)
		require.NotEmpty(t, got.AccessToken)
	})

	t.Run("audit failure keeps the failed-login error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		auth := newTestAuth("secret", false, userRepo, NewMockauthRepository(ctrl)).WithAudit(&captureRecorder{err: errAudit})
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)

		_, err := auth.LoginWithEmailPassword(context.Background(), "user@example.com", "pw")
		require.EqualError(t, err, "invalid email or password")
	})

	t.Run("session failure records nothing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		authRepo := NewMockauthRepository(ctrl)
		rec := &captureRecorder{}
		auth := newTestAuth("secret", true, userRepo, authRepo).WithAudit(rec)
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)
		authRepo.EXPECT().CreateRefreshSession(gomock.Any(), gomock.Any()).Return(int64(0), errors.New("db"))

		_, err := auth.LoginWithEmailPassword(context.Background(), "user@example.com", "pw")
		require.EqualError(t, err, "db")
		require.Empty(t, rec.entries)
	})
}

type sshAuditCase struct {
	userRepo *MockuserRepository
	authRepo *MockauthRepository
	keyRepo  *MockauthSSHKeyRepository
	rec      *captureRecorder
	auth     *Auth
	signer   ssh.Signer
}

func newSSHAuditCase(t *testing.T, email string) *sshAuditCase {
	ctrl := gomock.NewController(t)
	c := &sshAuditCase{
		userRepo: NewMockuserRepository(ctrl),
		authRepo: NewMockauthRepository(ctrl),
		keyRepo:  NewMockauthSSHKeyRepository(ctrl),
		rec:      &captureRecorder{},
		signer:   sshLoginTestSigner(t),
	}
	c.auth = newTestAuth("secret", true, c.userRepo, c.authRepo).WithSSHKeys(c.keyRepo).WithAudit(c.rec)
	c.authRepo.EXPECT().ConsumeSSHLoginChallenge(gomock.Any(), "nonce").Return(&domain.SSHLoginChallenge{
		Email: email, Nonce: "nonce", ExpiresAt: time.Now().Add(30 * time.Second),
	}, nil)
	return c
}

func (c *sshAuditCase) complete(t *testing.T, email, signed string) error {
	_, err := c.auth.CompleteSSHKeyLogin(context.Background(), email, "nonce",
		c.signer.PublicKey().Marshal(), sshLoginTestSignature(t, c.signer, sshkey.LoginPayload(testSSHAudience, signed)))
	return err
}

func TestAuth_SSHLoginAudit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := newSSHAuditCase(t, "")
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 42}, nil)
		c.userRepo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, EmailVerified: true}, nil)
		c.authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(42))).Return(int64(1), nil)

		require.NoError(t, c.complete(t, "", "nonce"))
		entry := c.rec.only(t)
		require.Equal(t, domain.AuditAuthLoginSucceeded, entry.Action)
		require.Equal(t, map[string]any{"method": LoginMethodSSHKey}, entry.Metadata)
		require.Equal(t, snow.ID(42), *entry.ActorUserID)
	})

	t.Run("bad signature targets the account", func(t *testing.T) {
		c := newSSHAuditCase(t, "alice@example.com")
		c.userRepo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(&domain.User{ID: 42, EmailVerified: true}, nil)
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 42}, nil)

		assertSSHLoginFailed(t, c.complete(t, "alice@example.com", "tampered"))
		entry := c.rec.only(t)
		require.Equal(t, domain.AuditAuthLoginFailed, entry.Action)
		require.Equal(t, snow.ID(42).Base36(), entry.TargetID)
		require.Equal(t, map[string]any{"method": LoginMethodSSHKey, "email": "alice@example.com", "reason": "invalid_signature"}, entry.Metadata)
	})

	t.Run("unknown key resolves the email target", func(t *testing.T) {
		c := newSSHAuditCase(t, "alice@example.com")
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound())
		c.userRepo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(&domain.User{ID: 42}, nil)

		assertSSHLoginFailed(t, c.complete(t, "alice@example.com", "nonce"))
		entry := c.rec.only(t)
		require.Equal(t, snow.ID(42).Base36(), entry.TargetID)
		require.Equal(t, "unknown_key", entry.Metadata["reason"])
	})

	t.Run("key of another account without email targets the key owner", func(t *testing.T) {
		c := newSSHAuditCase(t, "")
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 7}, nil)
		c.userRepo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)

		assertSSHLoginFailed(t, c.complete(t, "", "nonce"))
		entry := c.rec.only(t)
		require.Equal(t, snow.ID(7).Base36(), entry.TargetID)
		require.Equal(t, "key_mismatch", entry.Metadata["reason"])
	})

	t.Run("unknown email has no target", func(t *testing.T) {
		c := newSSHAuditCase(t, "ghost@example.com")
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 7}, nil)
		c.userRepo.EXPECT().GetByEmail(gomock.Any(), "ghost@example.com").Return(nil, domain.NewErrorNotFound("x")).Times(2)

		assertSSHLoginFailed(t, c.complete(t, "ghost@example.com", "nonce"))
		require.Empty(t, c.rec.only(t).TargetID)
	})

	t.Run("lookup errors are not audited", func(t *testing.T) {
		c := newSSHAuditCase(t, "alice@example.com")
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 42}, nil)
		c.userRepo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(nil, errors.New("db"))

		require.EqualError(t, c.complete(t, "alice@example.com", "nonce"), "db")
		require.Empty(t, c.rec.entries)
	})

	t.Run("audit failure does not fail the login", func(t *testing.T) {
		c := newSSHAuditCase(t, "")
		c.rec.err = errAudit
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 42}, nil)
		c.userRepo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, EmailVerified: true}, nil)
		c.authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(42))).Return(int64(1), nil)

		require.NoError(t, c.complete(t, "", "nonce"))
	})
}

func TestAuth_SSHLoginAuditInvalidChallengeAndKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	rec := &captureRecorder{}
	auth := newTestAuth("secret", true, NewMockuserRepository(ctrl), authRepo).WithSSHKeys(NewMockauthSSHKeyRepository(ctrl)).WithAudit(rec)

	authRepo.EXPECT().ConsumeSSHLoginChallenge(gomock.Any(), "gone").Return(nil, domain.NewErrorRecordNotFound())
	_, err := auth.CompleteSSHKeyLogin(context.Background(), "", "gone", []byte("k"), []byte("s"))
	assertSSHLoginFailed(t, err)

	authRepo.EXPECT().ConsumeSSHLoginChallenge(gomock.Any(), "n").Return(&domain.SSHLoginChallenge{Nonce: "n", ExpiresAt: time.Now().Add(time.Minute)}, nil)
	_, err = auth.CompleteSSHKeyLogin(context.Background(), "", "n", []byte("not a key"), []byte("s"))
	assertSSHLoginFailed(t, err)

	require.Len(t, rec.entries, 2)
	require.Equal(t, "invalid_challenge", rec.entries[0].Metadata["reason"])
	require.Equal(t, "invalid_key", rec.entries[1].Metadata["reason"])
}

func TestAuth_LoginResultRecordsOnlyViaMethod(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	rec := &captureRecorder{}
	auth := NewAuth("secret", nil, nil, authRepo).WithAudit(rec)
	authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(5))).Return(int64(1), nil).Times(3)
	user := &domain.User{ID: 5}

	_, err := auth.LoginResult(context.Background(), user)
	require.NoError(t, err)
	require.Empty(t, rec.entries)

	_, err = auth.LoginResultVia(context.Background(), user, LoginMethodOAuth)
	require.NoError(t, err)
	_, err = auth.LoginResultVia(context.Background(), user, LoginMethodEmailVerification)
	require.NoError(t, err)
	require.Len(t, rec.entries, 2)
	require.Equal(t, LoginMethodOAuth, rec.entries[0].Metadata["method"])
	require.Equal(t, LoginMethodEmailVerification, rec.entries[1].Metadata["method"])
	for _, entry := range rec.entries {
		require.Equal(t, domain.AuditAuthLoginSucceeded, entry.Action)
	}

	authRepo.EXPECT().CreateRefreshSession(gomock.Any(), gomock.Any()).Return(int64(0), errors.New("db"))
	_, err = auth.LoginResultVia(context.Background(), user, LoginMethodOAuth)
	require.EqualError(t, err, "db")
	require.Len(t, rec.entries, 2)
}

func TestAuth_PasswordLoginNormalizesEmail(t *testing.T) {
	user := &domain.User{ID: 42, Email: "user@example.com", Password: "hash", EmailVerified: true}

	for _, input := range []string{"User@Example.COM", "  user@example.com\t", " USER@example.com "} {
		t.Run(input, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			userRepo := NewMockuserRepository(ctrl)
			authRepo := NewMockauthRepository(ctrl)
			auth := newTestAuth("secret", true, userRepo, authRepo)
			userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)
			authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(42))).Return(int64(1), nil)

			result, err := auth.LoginWithEmailPassword(context.Background(), input, "pw")
			require.NoError(t, err)
			require.NotEmpty(t, result.AccessToken)
		})
	}

	t.Run("failure audits the normalized email", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		userRepo := NewMockuserRepository(ctrl)
		rec := &captureRecorder{}
		auth := newTestAuth("secret", false, userRepo, NewMockauthRepository(ctrl)).WithAudit(rec)
		userRepo.EXPECT().GetByEmail(gomock.Any(), "user@example.com").Return(user, nil)

		_, err := auth.LoginWithEmailPassword(context.Background(), " User@Example.com ", "pw")
		require.EqualError(t, err, "invalid email or password")
		require.Equal(t, map[string]any{"method": LoginMethodPassword, "email": "user@example.com", "reason": "invalid_credentials"}, rec.only(t).Metadata)
	})
}

func TestAuth_LoginThrottleSharesBucketAcrossEmailSpellings(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), "victim@example.com").Return(nil, errors.New("boom")).Times(2)
	auth := NewAuth("secret", nil, userRepo, NewMockauthRepository(ctrl)).WithLimiter(loginLimiter(2, 5))

	_, err := auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "victim@example.com", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), " Victim@Example.com ", "x")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ipCtx("10.0.0.1"), "VICTIM@EXAMPLE.COM", "x")
	requireTooMany(t, err)
}

func TestAuth_SSHLoginNormalizesEmail(t *testing.T) {
	t.Run("begin stores the normalized email", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		authRepo := NewMockauthRepository(ctrl)
		auth := newTestAuth("secret", true, NewMockuserRepository(ctrl), authRepo)
		authRepo.EXPECT().DeleteExpiredSSHLoginChallenges(gomock.Any(), gomock.Any()).Return(nil)
		authRepo.EXPECT().CreateSSHLoginChallenge(gomock.Any(), "alice@example.com", gomock.Any(), gomock.Any()).Return(nil)

		_, err := auth.BeginSSHKeyLogin(context.Background(), "  Alice@Example.com ")
		require.NoError(t, err)
	})

	for _, tc := range []struct{ name, stored, input string }{
		{"mixed case input", "alice@example.com", "Alice@Example.COM"},
		{"padded input", "alice@example.com", "  alice@example.com "},
		{"challenge stored before normalization", "Alice@Example.com", "Alice@Example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newSSHAuditCase(t, tc.stored)
			c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 42}, nil)
			c.userRepo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(&domain.User{ID: 42, EmailVerified: true}, nil)
			c.authRepo.EXPECT().CreateRefreshSession(gomock.Any(), sessionFor(snow.ID(42))).Return(int64(1), nil)

			require.NoError(t, c.complete(t, tc.input, "nonce"))
			require.Equal(t, domain.AuditAuthLoginSucceeded, c.rec.only(t).Action)
		})
	}

	t.Run("failure audits the normalized email", func(t *testing.T) {
		c := newSSHAuditCase(t, "alice@example.com")
		c.keyRepo.EXPECT().GetByFingerprint(gomock.Any(), gomock.Any()).Return(&domain.SSHPublicKey{UserID: 42}, nil)
		c.userRepo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(&domain.User{ID: 42, EmailVerified: true}, nil)

		assertSSHLoginFailed(t, c.complete(t, " Alice@Example.com ", "tampered"))
		require.Equal(t, map[string]any{"method": LoginMethodSSHKey, "email": "alice@example.com", "reason": "invalid_signature"}, c.rec.only(t).Metadata)
	})

	t.Run("a different account still mismatches", func(t *testing.T) {
		c := newSSHAuditCase(t, "bob@example.com")
		c.userRepo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(nil, domain.NewErrorNotFound("x"))
		assertSSHLoginFailed(t, c.complete(t, "Alice@Example.com", "nonce"))
		require.Equal(t, "invalid_challenge", c.rec.only(t).Metadata["reason"])
	})

	t.Run("begin and complete share the throttle bucket", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl)).
			WithSSHAudience(testSSHAudience).WithLimiter(loginLimiter(1, 10))
		_, err := auth.CompleteSSHKeyLogin(ipCtx("10.0.0.1"), "a@example.com", "", nil, nil)
		requireNotTooMany(t, err)
		_, err = auth.BeginSSHKeyLogin(ipCtx("10.0.0.1"), " A@Example.com ")
		requireTooMany(t, err)
	})
}
