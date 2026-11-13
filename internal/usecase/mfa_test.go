package usecase

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/totp"
)

type fakeMFARepo struct {
	mu         sync.Mutex
	totp       map[snow.ID]*domain.UserTOTP
	codes      map[snow.ID]map[string]bool
	challenges map[string]*domain.MFAChallenge
	err        error
}

func newFakeMFARepo() *fakeMFARepo {
	return &fakeMFARepo{
		totp:       map[snow.ID]*domain.UserTOTP{},
		codes:      map[snow.ID]map[string]bool{},
		challenges: map[string]*domain.MFAChallenge{},
	}
}

func (f *fakeMFARepo) GetTOTP(_ context.Context, userID snow.ID) (*domain.UserTOTP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	row, ok := f.totp[userID]
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	copied := *row
	return &copied, nil
}

func (f *fakeMFARepo) SavePendingTOTP(_ context.Context, userID snow.ID, secretEnc []byte, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.totp[userID]; ok && row.Confirmed() {
		return false, nil
	}
	f.totp[userID] = &domain.UserTOTP{UserID: userID, SecretEnc: secretEnc, CreatedAt: at}
	return true, nil
}

func (f *fakeMFARepo) ConfirmTOTP(_ context.Context, userID snow.ID, secretEnc []byte, step int64, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.totp[userID]
	if !ok || row.Confirmed() || !bytes.Equal(row.SecretEnc, secretEnc) {
		return false, nil
	}
	row.ConfirmedAt = &at
	row.LastStep = step
	return true, nil
}

func (f *fakeMFARepo) AdvanceTOTPStep(_ context.Context, userID snow.ID, step int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.totp[userID]
	if !ok || !row.Confirmed() || row.LastStep >= step {
		return false, nil
	}
	row.LastStep = step
	return true, nil
}

func (f *fakeMFARepo) DeleteTOTP(_ context.Context, userID snow.ID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.totp[userID]
	delete(f.totp, userID)
	return ok, nil
}

func (f *fakeMFARepo) CountConfirmedTOTP(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	var n int64
	for _, row := range f.totp {
		if row.Confirmed() {
			n++
		}
	}
	return n, nil
}

func (f *fakeMFARepo) ReplaceRecoveryCodes(_ context.Context, userID snow.ID, hashes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[userID] = map[string]bool{}
	for _, h := range hashes {
		f.codes[userID][h] = false
	}
	return nil
}

func (f *fakeMFARepo) DeleteRecoveryCodes(_ context.Context, userID snow.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.codes, userID)
	return nil
}

func (f *fakeMFARepo) UseRecoveryCode(_ context.Context, userID snow.ID, hash string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used, ok := f.codes[userID][hash]
	if !ok || used {
		return false, nil
	}
	f.codes[userID][hash] = true
	return true, nil
}

func (f *fakeMFARepo) CountRecoveryCodes(_ context.Context, userID snow.ID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, used := range f.codes[userID] {
		if !used {
			n++
		}
	}
	return n, nil
}

func (f *fakeMFARepo) CreateChallenge(_ context.Context, c domain.MFAChallenge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenges[c.TokenHash] = &c
	return nil
}

func (f *fakeMFARepo) DeleteExpiredChallenges(_ context.Context, before time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, c := range f.challenges {
		if c.ExpiresAt.Before(before) {
			delete(f.challenges, k)
		}
	}
	return nil
}

func (f *fakeMFARepo) GetChallenge(_ context.Context, tokenHash string) (*domain.MFAChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.challenges[tokenHash]
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	copied := *c
	return &copied, nil
}

func (f *fakeMFARepo) AddChallengeAttempt(_ context.Context, tokenHash string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.challenges[tokenHash]
	if !ok {
		return 0, domain.NewErrorRecordNotFound()
	}
	c.Attempts++
	return c.Attempts, nil
}

func (f *fakeMFARepo) DeleteChallenge(_ context.Context, tokenHash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.challenges[tokenHash]
	delete(f.challenges, tokenHash)
	return ok, nil
}

func (f *fakeMFARepo) DeleteUserChallenges(_ context.Context, userID snow.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, c := range f.challenges {
		if c.UserID == userID {
			delete(f.challenges, k)
		}
	}
	return nil
}

type fakeUsers map[snow.ID]*domain.User

func (f fakeUsers) GetByID(_ context.Context, id snow.ID) (*domain.User, error) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (f fakeUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, u := range f {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, domain.NewErrorRecordNotFound()
}

type recordedNotice struct {
	user   snow.ID
	notice string
}

type captureMFANotifier struct {
	notices []recordedNotice
}

func (c *captureMFANotifier) NotifyMFA(_ context.Context, user *domain.User, notice string) {
	c.notices = append(c.notices, recordedNotice{user.ID, notice})
}

type mfaFixture struct {
	repo     *fakeMFARepo
	users    fakeUsers
	mfa      *MFA
	auth     *Auth
	sessions *fakeRefreshRepo
	audit    *captureRecorder
	notifier *captureMFANotifier
	now      time.Time
	cipher   *totp.Cipher
}

const (
	mfaAlice snow.ID = 42
	mfaBob   snow.ID = 43
	mfaAdmin snow.ID = 44
)

func newMFAFixture(t *testing.T, available bool) *mfaFixture {
	t.Helper()
	f := &mfaFixture{
		repo: newFakeMFARepo(),
		users: fakeUsers{
			mfaAlice: {ID: mfaAlice, Email: "alice@example.com", Password: "hash", EmailVerified: true},
			mfaBob:   {ID: mfaBob, Email: "bob@example.com", EmailVerified: true},
			mfaAdmin: {ID: mfaAdmin, Email: "admin@example.com", Password: "hash", EmailVerified: true, IsAdmin: true},
		},
		sessions: newFakeRefreshRepo(),
		audit:    &captureRecorder{},
		notifier: &captureMFANotifier{},
		now:      time.Unix(1_800_000_000, 0),
	}
	if available {
		c, err := totp.NewCipher(bytes.Repeat([]byte{9}, 32))
		require.NoError(t, err)
		f.cipher = c
	}
	f.mfa = NewMFA(f.repo, f.users, stubPasswordHasher{match: true}, f.cipher, f.audit).
		WithAudit(f.audit).WithNotifier(f.notifier).WithClock(func() time.Time { return f.now })
	f.auth = NewAuth("secret", stubPasswordHasher{match: true}, f.users, f.sessions).
		WithMFA(f.mfa).WithAudit(f.audit).WithClock(func() time.Time { return f.now })
	return f
}

func userCtx(id snow.ID) context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: id})
}

func (f *mfaFixture) code(t *testing.T, secret []byte, delta int64) string {
	t.Helper()
	return totp.Code(secret, totp.Step(f.now)+delta)
}

// enroll runs the whole enrollment for a user and returns the plain secret
// and the recovery codes.
func (f *mfaFixture) enroll(t *testing.T, id snow.ID) ([]byte, []string) {
	t.Helper()
	ctx := userCtx(id)
	if f.users[id].Password == "" {
		ctx = domain.ContextWithClaim(context.Background(), domain.Claims{UserID: id, AuthTime: jwt.NewNumericDate(f.now)})
	}
	enrollment, err := f.mfa.StartTOTP(ctx, "password")
	require.NoError(t, err)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	require.NoError(t, err)
	require.Contains(t, enrollment.URI, "otpauth://totp/Nipa:")
	codes, err := f.mfa.ConfirmTOTP(ctx, f.code(t, secret, 0))
	require.NoError(t, err)
	require.Len(t, codes.Codes, totp.RecoveryCodeCount)
	f.now = f.now.Add(totp.Period * time.Second)
	return secret, codes.Codes
}

func requireCode(t *testing.T, err error, code int) {
	t.Helper()
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, code, derr.Code, derr.Message)
}

func TestMFA_EnrollAndStatus(t *testing.T) {
	f := newMFAFixture(t, true)
	ctx := userCtx(mfaAlice)

	status, err := f.mfa.Status(ctx)
	require.NoError(t, err)
	require.True(t, status.Available)
	require.False(t, status.TOTPEnabled)

	_, err = f.mfa.ConfirmTOTP(ctx, "123456")
	requireCode(t, err, 400)

	f.enroll(t, mfaAlice)

	status, err = f.mfa.Status(ctx)
	require.NoError(t, err)
	require.True(t, status.TOTPEnabled)
	require.NotNil(t, status.ConfirmedAt)
	require.EqualValues(t, totp.RecoveryCodeCount, status.RecoveryCodesLeft)

	_, err = f.mfa.StartTOTP(ctx, "password")
	requireCode(t, err, 409)
	_, err = f.mfa.ConfirmTOTP(ctx, "123456")
	requireCode(t, err, 409)

	require.Equal(t, domain.AuditUserMFAEnabled, f.audit.only(t).Action)
	require.Equal(t, []recordedNotice{{mfaAlice, MFANoticeEnabled}}, f.notifier.notices)
}

func TestMFA_StartReplacesPendingAndChecksCode(t *testing.T) {
	f := newMFAFixture(t, true)
	ctx := userCtx(mfaAlice)

	first, err := f.mfa.StartTOTP(ctx, "password")
	require.NoError(t, err)
	second, err := f.mfa.StartTOTP(ctx, "password")
	require.NoError(t, err)
	require.NotEqual(t, first.Secret, second.Secret)

	oldSecret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(first.Secret)
	_, err = f.mfa.ConfirmTOTP(ctx, f.code(t, oldSecret, 0))
	requireCode(t, err, 400)

	status, err := f.mfa.Status(ctx)
	require.NoError(t, err)
	require.False(t, status.TOTPEnabled, "an unconfirmed secret does not enable MFA")
}

func TestMFA_StartReauthentication(t *testing.T) {
	t.Run("password required", func(t *testing.T) {
		f := newMFAFixture(t, true)
		_, err := f.mfa.StartTOTP(userCtx(mfaAlice), "")
		requireCode(t, err, 400)
	})
	t.Run("wrong password", func(t *testing.T) {
		f := newMFAFixture(t, true)
		f.mfa.hasher = stubPasswordHasher{match: false}
		_, err := f.mfa.StartTOTP(userCtx(mfaAlice), "nope")
		requireCode(t, err, 400)
	})
	t.Run("passwordless needs a recent sign-in", func(t *testing.T) {
		f := newMFAFixture(t, true)
		_, err := f.mfa.StartTOTP(userCtx(mfaBob), "")
		requireCode(t, err, 403)
		stale := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: mfaBob, AuthTime: jwt.NewNumericDate(f.now.Add(-11 * time.Minute))})
		_, err = f.mfa.StartTOTP(stale, "")
		requireCode(t, err, 403)
		f.enroll(t, mfaBob)
	})
	t.Run("throttled", func(t *testing.T) {
		f := newMFAFixture(t, true)
		f.mfa.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{ratelimit.ScopeAuthID: {Rate: 0.0001, Burst: 1}}))
		_, err := f.mfa.StartTOTP(userCtx(mfaAlice), "password")
		require.NoError(t, err)
		_, err = f.mfa.StartTOTP(userCtx(mfaAlice), "password")
		requireCode(t, err, 429)
	})
	t.Run("unauthenticated", func(t *testing.T) {
		f := newMFAFixture(t, true)
		_, err := f.mfa.StartTOTP(context.Background(), "password")
		requireCode(t, err, 401)
		_, err = f.mfa.Status(context.Background())
		requireCode(t, err, 401)
	})
}

func TestMFA_Unavailable(t *testing.T) {
	f := newMFAFixture(t, false)
	ctx := userCtx(mfaAlice)

	status, err := f.mfa.Status(ctx)
	require.NoError(t, err)
	require.False(t, status.Available)
	_, err = f.mfa.StartTOTP(ctx, "password")
	requireCode(t, err, 503)
	_, err = f.mfa.ConfirmTOTP(ctx, "123456")
	requireCode(t, err, 503)
}

func TestMFA_KeyRemovedAfterEnrollment(t *testing.T) {
	f := newMFAFixture(t, true)
	_, codes := f.enroll(t, mfaAlice)
	f.mfa.cipher = nil

	_, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	requireCode(t, err, 503)

	err = f.mfa.DisableTOTP(userCtx(mfaAlice), "123456")
	requireCode(t, err, 503)
	require.NoError(t, f.mfa.DisableTOTP(userCtx(mfaAlice), codes[0]), "a recovery code needs no key")
}

func TestMFA_WrongKeyIsServerError(t *testing.T) {
	f := newMFAFixture(t, true)
	secret, _ := f.enroll(t, mfaAlice)
	other, err := totp.NewCipher(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	f.mfa.cipher = other

	login, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	_, err = f.auth.CompleteLoginMFA(context.Background(), login.MFAChallenge.Token, f.code(t, secret, 0))
	requireCode(t, err, 500)
}

func TestMFA_LogUnusableEnrollments(t *testing.T) {
	f := newMFAFixture(t, true)
	f.enroll(t, mfaAlice)
	f.mfa.LogUnusableEnrollments(context.Background())
	f.mfa.cipher = nil
	f.mfa.LogUnusableEnrollments(context.Background())
	f.repo.err = errors.New("db down")
	f.mfa.LogUnusableEnrollments(context.Background())
}

func TestMFA_Disable(t *testing.T) {
	f := newMFAFixture(t, true)
	ctx := userCtx(mfaAlice)

	requireCode(t, f.mfa.DisableTOTP(ctx, "123456"), 400)

	secret, _ := f.enroll(t, mfaAlice)
	requireCode(t, f.mfa.DisableTOTP(ctx, "000000"), 400)
	requireCode(t, f.mfa.DisableTOTP(ctx, "not-a-code"), 400)
	require.NoError(t, f.mfa.DisableTOTP(ctx, f.code(t, secret, 0)))

	status, err := f.mfa.Status(ctx)
	require.NoError(t, err)
	require.False(t, status.TOTPEnabled)
	require.Empty(t, f.repo.codes[mfaAlice])
	require.Equal(t, domain.AuditUserMFADisabled, f.audit.entries[len(f.audit.entries)-1].Action)
	require.Equal(t, MFANoticeDisabled, f.notifier.notices[len(f.notifier.notices)-1].notice)
}

func TestMFA_RegenerateRecoveryCodes(t *testing.T) {
	f := newMFAFixture(t, true)
	ctx := userCtx(mfaAlice)
	_, err := f.mfa.RegenerateRecoveryCodes(ctx, "123456")
	requireCode(t, err, 400)

	secret, codes := f.enroll(t, mfaAlice)
	fresh, err := f.mfa.RegenerateRecoveryCodes(ctx, codes[0])
	require.NoError(t, err)
	require.Len(t, fresh.Codes, totp.RecoveryCodeCount)
	require.Equal(t, MFANoticeRecoveryCodeUsed, f.notifier.notices[len(f.notifier.notices)-1].notice)

	_, err = f.mfa.RegenerateRecoveryCodes(ctx, codes[1])
	requireCode(t, err, 400)

	again, err := f.mfa.RegenerateRecoveryCodes(ctx, f.code(t, secret, 0))
	require.NoError(t, err)
	require.NotEqual(t, fresh.Codes, again.Codes)
	require.Equal(t, domain.AuditUserMFARecoveryCodesRegenerated, f.audit.entries[len(f.audit.entries)-1].Action)

	_, err = f.mfa.RegenerateRecoveryCodes(ctx, f.code(t, secret, 0))
	requireCode(t, err, 400)
}

func TestMFA_AdminReset(t *testing.T) {
	f := newMFAFixture(t, true)
	f.enroll(t, mfaAlice)

	requireCode(t, f.mfa.AdminReset(userCtx(mfaBob), mfaAlice), 403)
	adminCtx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: mfaAdmin, IsAdmin: true})
	require.NoError(t, f.mfa.AdminReset(adminCtx, mfaAlice))
	requireCode(t, f.mfa.AdminReset(adminCtx, mfaAlice), 400)
	requireCode(t, f.mfa.AdminReset(adminCtx, snow.ID(999)), 404)

	enabled, err := f.mfa.required(context.Background(), mfaAlice)
	require.NoError(t, err)
	require.False(t, enabled)
	require.Equal(t, domain.AuditUserMFAResetByAdmin, f.audit.entries[len(f.audit.entries)-1].Action)
	require.Equal(t, MFANoticeResetByAdmin, f.notifier.notices[len(f.notifier.notices)-1].notice)
}

func TestAuth_LoginWithoutMFAIssuesSession(t *testing.T) {
	f := newMFAFixture(t, true)
	result, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	require.Nil(t, result.MFAChallenge)
	require.NotEmpty(t, result.AccessToken)

	claims, err := f.auth.ValidateToken(context.Background(), result.AccessToken)
	require.NoError(t, err)
	require.Equal(t, []string{LoginMethodPassword}, claims.AuthMethods)
	require.NotNil(t, claims.AuthTime)
}

func TestAuth_LoginWithTOTP(t *testing.T) {
	f := newMFAFixture(t, true)
	secret, _ := f.enroll(t, mfaAlice)

	result, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	require.NotNil(t, result.MFAChallenge)
	require.Empty(t, result.AccessToken)
	require.Empty(t, result.RefreshToken)
	require.Equal(t, 300, result.MFAChallenge.ExpiresIn)

	code := f.code(t, secret, 0)
	done, err := f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, code)
	require.NoError(t, err)
	require.NotEmpty(t, done.AccessToken)

	claims, err := f.auth.ValidateToken(context.Background(), done.AccessToken)
	require.NoError(t, err)
	require.Equal(t, []string{LoginMethodPassword, domain.MFATOTP}, claims.AuthMethods)

	last := f.audit.entries[len(f.audit.entries)-1]
	require.Equal(t, domain.AuditAuthLoginSucceeded, last.Action)
	require.Equal(t, domain.MFATOTP, last.Metadata["mfa"])

	_, err = f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, code)
	requireCode(t, err, 400)

	again, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	_, err = f.auth.CompleteLoginMFA(context.Background(), again.MFAChallenge.Token, code)
	requireCode(t, err, 400)
	failed := f.audit.entries[len(f.audit.entries)-1]
	require.Equal(t, domain.AuditAuthLoginFailed, failed.Action)
	require.Equal(t, "mfa_code", failed.Metadata["reason"])
	require.Equal(t, "alice@example.com", failed.Metadata["email"])

	refreshed, err := f.auth.LoginWithRefreshToken(context.Background(), done.RefreshToken)
	require.NoError(t, err)
	claims, err = f.auth.ValidateToken(context.Background(), refreshed.AccessToken)
	require.NoError(t, err)
	require.Equal(t, []string{LoginMethodPassword, domain.MFATOTP}, claims.AuthMethods, "a refresh keeps the factors")

	reissued, err := f.auth.LoginResult(domain.ContextWithClaim(context.Background(), *claims), f.users[mfaAlice])
	require.NoError(t, err)
	claims, err = f.auth.ValidateToken(context.Background(), reissued.AccessToken)
	require.NoError(t, err)
	require.Equal(t, []string{LoginMethodPassword, domain.MFATOTP}, claims.AuthMethods)
}

func TestAuth_LoginWithRecoveryCode(t *testing.T) {
	f := newMFAFixture(t, true)
	_, codes := f.enroll(t, mfaAlice)

	result, err := f.auth.LoginWithFirstFactor(context.Background(), f.users[mfaAlice], LoginMethodOAuth)
	require.NoError(t, err)
	done, err := f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, codes[3])
	require.NoError(t, err)
	claims, err := f.auth.ValidateToken(context.Background(), done.AccessToken)
	require.NoError(t, err)
	require.Equal(t, []string{LoginMethodOAuth, domain.MFARecoveryCode}, claims.AuthMethods)
	require.Equal(t, MFANoticeRecoveryCodeUsed, f.notifier.notices[len(f.notifier.notices)-1].notice)

	status, err := f.mfa.Status(userCtx(mfaAlice))
	require.NoError(t, err)
	require.EqualValues(t, totp.RecoveryCodeCount-1, status.RecoveryCodesLeft)

	again, err := f.auth.LoginWithFirstFactor(context.Background(), f.users[mfaAlice], LoginMethodOAuth)
	require.NoError(t, err)
	_, err = f.auth.CompleteLoginMFA(context.Background(), again.MFAChallenge.Token, codes[3])
	requireCode(t, err, 400)
}

func TestAuth_MFAAttemptCap(t *testing.T) {
	f := newMFAFixture(t, true)
	secret, _ := f.enroll(t, mfaAlice)
	result, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	token := result.MFAChallenge.Token

	for i := 0; i < mfaMaxAttempts; i++ {
		_, err := f.auth.CompleteLoginMFA(context.Background(), token, "000000")
		requireCode(t, err, 400)
	}
	require.Empty(t, f.repo.challenges, "the fifth failure deletes the challenge")
	_, err = f.auth.CompleteLoginMFA(context.Background(), token, f.code(t, secret, 0))
	requireCode(t, err, 400)
}

func TestAuth_MFAChallengeEdges(t *testing.T) {
	f := newMFAFixture(t, true)
	secret, _ := f.enroll(t, mfaAlice)

	_, err := f.auth.CompleteLoginMFA(context.Background(), "", "123456")
	requireCode(t, err, 400)
	_, err = f.auth.CompleteLoginMFA(context.Background(), "unknown", "123456")
	requireCode(t, err, 400)

	result, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	f.now = f.now.Add(mfaChallengeTTL + time.Second)
	_, err = f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, f.code(t, secret, 0))
	requireCode(t, err, 400)
	require.Empty(t, f.repo.challenges)

	result, err = f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	f.users[mfaAlice].EmailVerified = false
	_, err = f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, f.code(t, secret, 0))
	requireCode(t, err, 403)

	plain := NewAuth("secret", stubPasswordHasher{match: true}, f.users, f.sessions)
	_, err = plain.CompleteLoginMFA(context.Background(), "x", "123456")
	requireCode(t, err, 400)
}

func TestAuth_MFAThrottled(t *testing.T) {
	f := newMFAFixture(t, true)
	secret, _ := f.enroll(t, mfaAlice)
	f.mfa.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{ratelimit.ScopeAuthID: {Rate: 0.0001, Burst: 1}}))

	result, err := f.auth.LoginWithEmailPassword(context.Background(), "alice@example.com", "password")
	require.NoError(t, err)
	_, err = f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, "000000")
	requireCode(t, err, 400)
	_, err = f.auth.CompleteLoginMFA(context.Background(), result.MFAChallenge.Token, f.code(t, secret, 0))
	requireCode(t, err, 429)
}

func TestMFA_Disabled(t *testing.T) {
	f := newMFAFixture(t, true)
	_, codes := f.enroll(t, mfaAlice)
	f.mfa.WithEnabled(false)
	ctx := userCtx(mfaAlice)

	status, err := f.mfa.Status(ctx)
	require.NoError(t, err)
	require.False(t, status.Available)
	require.True(t, status.TOTPEnabled)

	_, err = f.mfa.StartTOTP(userCtx(mfaBob), "password")
	requireCode(t, err, 503)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Contains(t, derr.Message, "disabled")
	_, err = f.mfa.ConfirmTOTP(ctx, "123456")
	requireCode(t, err, 503)

	f.mfa.LogUnusableEnrollments(context.Background())
	require.NoError(t, f.mfa.DisableTOTP(ctx, codes[0]), "leftover enrollments can still be removed")

	f.mfa.WithEnabled(true)
	require.True(t, f.mfa.Available())
}
