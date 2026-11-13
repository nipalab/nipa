package usecase

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/totp"
	"github.com/nipalab/nipa/internal/usecase/dto"
)

const (
	mfaChallengeTTL   = 5 * time.Minute
	mfaChallengeBytes = 32
	mfaMaxAttempts    = 5
	mfaRecentSignIn   = 10 * time.Minute
	mfaIssuer         = "Nipa"
)

// Notifications sent to the account owner when its second factor changes.
const (
	MFANoticeEnabled          = "enabled"
	MFANoticeDisabled         = "disabled"
	MFANoticeResetByAdmin     = "reset_by_admin"
	MFANoticeRecoveryCodeUsed = "recovery_code_used"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=mfa_mock_test.go -package=usecase
type mfaRepository interface {
	GetTOTP(ctx context.Context, userID snow.ID) (*domain.UserTOTP, error)
	SavePendingTOTP(ctx context.Context, userID snow.ID, secretEnc []byte, at time.Time) (bool, error)
	ConfirmTOTP(ctx context.Context, userID snow.ID, secretEnc []byte, step int64, at time.Time) (bool, error)
	AdvanceTOTPStep(ctx context.Context, userID snow.ID, step int64) (bool, error)
	DeleteTOTP(ctx context.Context, userID snow.ID) (bool, error)
	CountConfirmedTOTP(ctx context.Context) (int64, error)
	ReplaceRecoveryCodes(ctx context.Context, userID snow.ID, hashes []string) error
	DeleteRecoveryCodes(ctx context.Context, userID snow.ID) error
	UseRecoveryCode(ctx context.Context, userID snow.ID, hash string, at time.Time) (bool, error)
	CountRecoveryCodes(ctx context.Context, userID snow.ID) (int64, error)
	CreateChallenge(ctx context.Context, challenge domain.MFAChallenge) error
	DeleteExpiredChallenges(ctx context.Context, before time.Time) error
	GetChallenge(ctx context.Context, tokenHash string) (*domain.MFAChallenge, error)
	AddChallengeAttempt(ctx context.Context, tokenHash string) (int64, error)
	DeleteChallenge(ctx context.Context, tokenHash string) (bool, error)
	DeleteUserChallenges(ctx context.Context, userID snow.ID) error
}

type mfaNotifier interface {
	NotifyMFA(ctx context.Context, user *domain.User, notice string)
}

// MFA manages TOTP second factors and checks codes for pending logins. A nil
// cipher (no MFA_ENCRYPTION_KEY) leaves the feature unavailable.
type MFA struct {
	repo     mfaRepository
	users    userRepository
	hasher   passwordHasher
	cipher   *totp.Cipher
	disabled bool
	tx       transactor
	audit    AuditRecorder
	limiter  *ratelimit.Limiter
	notifier mfaNotifier
	now      func() time.Time
}

func NewMFA(repo mfaRepository, users userRepository, hasher passwordHasher, cipher *totp.Cipher, tx transactor) *MFA {
	return &MFA{repo: repo, users: users, hasher: hasher, cipher: cipher, tx: tx, now: time.Now}
}

// WithEnabled turns the feature off when MFA_ENABLED is false: nobody can
// enroll and logins never ask for a code.
func (m *MFA) WithEnabled(enabled bool) *MFA {
	m.disabled = !enabled
	return m
}

func (m *MFA) WithAudit(rec AuditRecorder) *MFA {
	m.audit = rec
	return m
}

func (m *MFA) WithLimiter(limiter *ratelimit.Limiter) *MFA {
	m.limiter = limiter
	return m
}

func (m *MFA) WithNotifier(n mfaNotifier) *MFA {
	m.notifier = n
	return m
}

func (m *MFA) WithClock(now func() time.Time) *MFA {
	m.now = now
	return m
}

func (m *MFA) Available() bool {
	return !m.disabled && m.cipher != nil
}

func (m *MFA) errUnavailable() error {
	if m.disabled {
		return domain.NewErrorServiceUnavailable("two-factor authentication is disabled on this server")
	}
	return domain.NewErrorServiceUnavailable("two-factor authentication is not configured on this server")
}

func errInvalidMFACode() error {
	return domain.NewErrorUser("invalid code")
}

func errMFANotEnabled() error {
	return domain.NewErrorUser("two-factor authentication is not enabled")
}

func (m *MFA) Status(ctx context.Context) (*dto.MFAStatus, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorUnauthorized("authentication required")
	}
	status := &dto.MFAStatus{Available: m.Available()}
	row, err := m.repo.GetTOTP(ctx, claim.UserID)
	if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if !row.Confirmed() {
		return status, nil
	}
	status.TOTPEnabled = true
	status.ConfirmedAt = row.ConfirmedAt
	if status.RecoveryCodesLeft, err = m.repo.CountRecoveryCodes(ctx, claim.UserID); err != nil {
		return nil, err
	}
	return status, nil
}

// StartTOTP creates (or replaces) an unconfirmed secret. It asks for the
// current password; a passwordless account must have signed in recently.
func (m *MFA) StartTOTP(ctx context.Context, password string) (*dto.TOTPEnrollment, error) {
	if !m.Available() {
		return nil, m.errUnavailable()
	}
	user, claim, err := m.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.reauthenticate(ctx, user, claim, password); err != nil {
		return nil, err
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	sealed, err := m.cipher.Seal(secret, secretAAD(user.ID))
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	saved, err := m.repo.SavePendingTOTP(ctx, user.ID, sealed, m.now())
	if err != nil {
		return nil, err
	}
	if !saved {
		return nil, domain.NewErrorConflict("two-factor authentication is already enabled")
	}
	return &dto.TOTPEnrollment{Secret: totp.EncodeSecret(secret), URI: totp.URI(mfaIssuer, user.Email, secret)}, nil
}

func (m *MFA) reauthenticate(ctx context.Context, user *domain.User, claim domain.Claims, password string) error {
	if user.Password == "" {
		if claim.AuthTime == nil || m.now().Sub(claim.AuthTime.Time) > mfaRecentSignIn {
			return domain.NewErrorForbidden("sign in again to set up two-factor authentication")
		}
		return nil
	}
	if password == "" {
		return domain.NewErrorUser("current password is required")
	}
	if m.limiter != nil {
		if err := allowLimit(ctx, m.limiter, ratelimit.ScopeAuthID, "user:"+user.ID.Base36()); err != nil {
			return err
		}
	}
	if !m.hasher.Compare(user.Password, password) {
		return domain.NewErrorUser("current password is incorrect")
	}
	return nil
}

// ConfirmTOTP enables MFA with the first valid code and returns the recovery
// codes, which are shown only this once.
func (m *MFA) ConfirmTOTP(ctx context.Context, code string) (*dto.RecoveryCodes, error) {
	if !m.Available() {
		return nil, m.errUnavailable()
	}
	user, _, err := m.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.throttleCode(ctx, user.ID); err != nil {
		return nil, err
	}
	row, err := m.repo.GetTOTP(ctx, user.ID)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorUser("start two-factor setup first")
		}
		return nil, err
	}
	if row.Confirmed() {
		return nil, domain.NewErrorConflict("two-factor authentication is already enabled")
	}
	secret, err := m.openSecret(ctx, row)
	if err != nil {
		return nil, err
	}
	step, ok := totp.Match(secret, code, m.now(), row.LastStep)
	if !ok {
		return nil, errInvalidMFACode()
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	err = m.write(ctx, func(ctx context.Context) error {
		confirmed, err := m.repo.ConfirmTOTP(ctx, user.ID, row.SecretEnc, step, m.now())
		if err != nil {
			return err
		}
		if !confirmed {
			return domain.NewErrorConflict("two-factor setup changed; start again")
		}
		return m.repo.ReplaceRecoveryCodes(ctx, user.ID, hashes)
	}, userAudit(domain.AuditUserMFAEnabled, user.ID, map[string]any{"method": domain.MFATOTP}))
	if err != nil {
		return nil, err
	}
	m.notify(ctx, user, MFANoticeEnabled)
	return &dto.RecoveryCodes{Codes: codes}, nil
}

// DisableTOTP removes the secret and the recovery codes; it takes a current
// TOTP or recovery code.
func (m *MFA) DisableTOTP(ctx context.Context, code string) error {
	user, _, err := m.currentUser(ctx)
	if err != nil {
		return err
	}
	if err := m.requireEnabled(ctx, user.ID); err != nil {
		return err
	}
	if err := m.throttleCode(ctx, user.ID); err != nil {
		return err
	}
	err = m.write(ctx, func(ctx context.Context) error {
		if _, err := m.consumeCode(ctx, user.ID, code); err != nil {
			return err
		}
		return m.removeAll(ctx, user.ID)
	}, userAudit(domain.AuditUserMFADisabled, user.ID, nil))
	if err != nil {
		return err
	}
	m.notify(ctx, user, MFANoticeDisabled)
	return nil
}

// RegenerateRecoveryCodes replaces every recovery code after checking a
// current code.
func (m *MFA) RegenerateRecoveryCodes(ctx context.Context, code string) (*dto.RecoveryCodes, error) {
	user, _, err := m.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.requireEnabled(ctx, user.ID); err != nil {
		return nil, err
	}
	if err := m.throttleCode(ctx, user.ID); err != nil {
		return nil, err
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	var kind string
	err = m.write(ctx, func(ctx context.Context) error {
		if kind, err = m.consumeCode(ctx, user.ID, code); err != nil {
			return err
		}
		return m.repo.ReplaceRecoveryCodes(ctx, user.ID, hashes)
	}, userAudit(domain.AuditUserMFARecoveryCodesRegenerated, user.ID, nil))
	if err != nil {
		return nil, err
	}
	if kind == domain.MFARecoveryCode {
		m.notify(ctx, user, MFANoticeRecoveryCodeUsed)
	}
	return &dto.RecoveryCodes{Codes: codes}, nil
}

// AdminReset removes another account's second factor, under the same rule as
// the other account administration.
func (m *MFA) AdminReset(ctx context.Context, userID snow.ID) error {
	if err := requireCanManageUser(ctx, m.users, userID); err != nil {
		return err
	}
	user, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	err = m.write(ctx, func(ctx context.Context) error {
		removed, err := m.repo.DeleteTOTP(ctx, userID)
		if err != nil {
			return err
		}
		if !removed {
			return errMFANotEnabled()
		}
		return m.removeAll(ctx, userID)
	}, userAudit(domain.AuditUserMFAResetByAdmin, userID, nil))
	if err != nil {
		return err
	}
	m.notify(ctx, user, MFANoticeResetByAdmin)
	return nil
}

// LogUnusableEnrollments reports enrolled accounts the server cannot ask for
// their second factor: with MFA_ENABLED off they sign in with the password
// alone, without a key they cannot sign in with a password at all.
func (m *MFA) LogUnusableEnrollments(ctx context.Context) {
	if m.Available() {
		return
	}
	n, err := m.repo.CountConfirmedTOTP(ctx)
	if err != nil {
		obs.Log(ctx).Warn("count two-factor accounts failed", "error", err)
		return
	}
	if n == 0 {
		return
	}
	if m.disabled {
		obs.Log(ctx).Warn("MFA_ENABLED is off but accounts use two-factor authentication; they sign in without their second factor", "accounts", n)
		return
	}
	obs.Log(ctx).Error("MFA_ENCRYPTION_KEY is not set but accounts use two-factor authentication; they cannot sign in with a password until it is restored or an admin resets them", "accounts", n)
}

func (m *MFA) required(ctx context.Context, userID snow.ID) (bool, error) {
	row, err := m.repo.GetTOTP(ctx, userID)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return row.Confirmed(), nil
}

func (m *MFA) beginChallenge(ctx context.Context, userID snow.ID, method string) (*dto.MFAChallenge, error) {
	if !m.Available() {
		return nil, m.errUnavailable()
	}
	buf := make([]byte, mfaChallengeBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	now := m.now()
	if err := m.repo.DeleteExpiredChallenges(ctx, now); err != nil {
		return nil, err
	}
	err := m.repo.CreateChallenge(ctx, domain.MFAChallenge{
		TokenHash: hashSubject(token),
		UserID:    userID,
		Method:    method,
		ExpiresAt: now.Add(mfaChallengeTTL),
	})
	if err != nil {
		return nil, err
	}
	return &dto.MFAChallenge{Token: token, ExpiresIn: int(mfaChallengeTTL.Seconds())}, nil
}

// verifyLogin checks a code for a pending login challenge and consumes both.
// On a wrong code it still returns the challenge so the caller can audit it.
func (m *MFA) verifyLogin(ctx context.Context, token, code string) (*domain.MFAChallenge, string, error) {
	if token == "" || code == "" {
		return nil, "", errInvalidMFACode()
	}
	tokenHash := hashSubject(token)
	challenge, err := m.repo.GetChallenge(ctx, tokenHash)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, "", errInvalidMFACode()
		}
		return nil, "", err
	}
	if !challenge.ExpiresAt.After(m.now()) {
		if _, err := m.repo.DeleteChallenge(ctx, tokenHash); err != nil {
			return nil, "", err
		}
		return nil, "", errInvalidMFACode()
	}
	if err := m.throttleCode(ctx, challenge.UserID); err != nil {
		return nil, "", err
	}
	attempts, err := m.repo.AddChallengeAttempt(ctx, tokenHash)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, "", errInvalidMFACode()
		}
		return nil, "", err
	}
	if attempts > mfaMaxAttempts {
		_, err := m.repo.DeleteChallenge(ctx, tokenHash)
		if err != nil {
			return nil, "", err
		}
		return challenge, "", errInvalidMFACode()
	}
	var kind string
	err = m.inTx(ctx, func(ctx context.Context) error {
		if kind, err = m.consumeCode(ctx, challenge.UserID, code); err != nil {
			return err
		}
		deleted, err := m.repo.DeleteChallenge(ctx, tokenHash)
		if err != nil {
			return err
		}
		if !deleted {
			return errInvalidMFACode()
		}
		return nil
	})
	if err != nil {
		if attempts >= mfaMaxAttempts && !domain.IsServerError(err) {
			if _, delErr := m.repo.DeleteChallenge(ctx, tokenHash); delErr != nil {
				return nil, "", delErr
			}
		}
		return challenge, "", err
	}
	if kind == domain.MFARecoveryCode {
		if user, err := m.users.GetByID(ctx, challenge.UserID); err == nil {
			m.notify(ctx, user, MFANoticeRecoveryCodeUsed)
		}
	}
	return challenge, kind, nil
}

// consumeCode accepts a TOTP code (recording its step so it cannot be
// replayed) or an unused recovery code (marking it used).
func (m *MFA) consumeCode(ctx context.Context, userID snow.ID, code string) (string, error) {
	normalized := totp.NormalizeCode(code)
	if totp.IsCode(normalized) {
		if !m.Available() {
			return "", m.errUnavailable()
		}
		row, err := m.repo.GetTOTP(ctx, userID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return "", errInvalidMFACode()
			}
			return "", err
		}
		if !row.Confirmed() {
			return "", errInvalidMFACode()
		}
		secret, err := m.openSecret(ctx, row)
		if err != nil {
			return "", err
		}
		step, ok := totp.Match(secret, normalized, m.now(), row.LastStep)
		if !ok {
			return "", errInvalidMFACode()
		}
		advanced, err := m.repo.AdvanceTOTPStep(ctx, userID, step)
		if err != nil {
			return "", err
		}
		if !advanced {
			return "", errInvalidMFACode()
		}
		return domain.MFATOTP, nil
	}
	if totp.NormalizeRecoveryCode(code) == "" {
		return "", errInvalidMFACode()
	}
	used, err := m.repo.UseRecoveryCode(ctx, userID, totp.HashRecoveryCode(code), m.now())
	if err != nil {
		return "", err
	}
	if !used {
		return "", errInvalidMFACode()
	}
	return domain.MFARecoveryCode, nil
}

func (m *MFA) openSecret(ctx context.Context, row *domain.UserTOTP) ([]byte, error) {
	secret, err := m.cipher.Open(row.SecretEnc, secretAAD(row.UserID))
	if err != nil {
		if errors.Is(err, totp.ErrDecrypt) {
			obs.Log(ctx).Error("TOTP secret does not decrypt with MFA_ENCRYPTION_KEY; an admin must reset this account", "user", row.UserID.Base36())
		}
		return nil, domain.NewErrorInternalServer("decrypt totp secret: " + err.Error())
	}
	return secret, nil
}

func (m *MFA) requireEnabled(ctx context.Context, userID snow.ID) error {
	enabled, err := m.required(ctx, userID)
	if err != nil {
		return err
	}
	if !enabled {
		return errMFANotEnabled()
	}
	return nil
}

func (m *MFA) removeAll(ctx context.Context, userID snow.ID) error {
	if _, err := m.repo.DeleteTOTP(ctx, userID); err != nil {
		return err
	}
	if err := m.repo.DeleteRecoveryCodes(ctx, userID); err != nil {
		return err
	}
	return m.repo.DeleteUserChallenges(ctx, userID)
}

func (m *MFA) throttleCode(ctx context.Context, userID snow.ID) error {
	if m.limiter == nil {
		return nil
	}
	return allowLimit(ctx, m.limiter, ratelimit.ScopeAuthID, "mfa:"+userID.Base36())
}

func (m *MFA) currentUser(ctx context.Context) (*domain.User, domain.Claims, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, claim, domain.NewErrorUnauthorized("authentication required")
	}
	user, err := m.users.GetByID(ctx, claim.UserID)
	if err != nil {
		return nil, claim, err
	}
	return user, claim, nil
}

func (m *MFA) inTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if m.tx == nil {
		return fn(ctx)
	}
	return m.tx.WithinTx(ctx, fn)
}

func (m *MFA) write(ctx context.Context, fn func(ctx context.Context) error, entry *domain.AuditEntry) error {
	return m.inTx(ctx, func(ctx context.Context) error {
		return auditedWrite(ctx, m.audit, fn, func() *domain.AuditEntry { return entry })
	})
}

func (m *MFA) notify(ctx context.Context, user *domain.User, notice string) {
	if m.notifier != nil {
		m.notifier.NotifyMFA(context.WithoutCancel(ctx), user, notice)
	}
}

func secretAAD(userID snow.ID) []byte {
	return []byte("user:" + userID.Base36())
}

func newRecoveryCodes() ([]string, []string, error) {
	codes, err := totp.NewRecoveryCodes(totp.RecoveryCodeCount)
	if err != nil {
		return nil, nil, domain.NewErrorInternalServer(err.Error())
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = totp.HashRecoveryCode(code)
	}
	return codes, hashes, nil
}
