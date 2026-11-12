package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/sshkey"
	"github.com/nipalab/nipa/internal/usecase/dto"
	"golang.org/x/crypto/ssh"
)

const (
	tokenExpirationMinutes = 30
	refreshTokenExpiration = 60 * 24 * time.Hour
	sshLoginChallengeTTL   = 60 * time.Second
	sshLoginChallengeBytes = 32
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=auth_mock_test.go -package=usecase
type userRepository interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id snow.ID) (*domain.User, error)
}

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=auth_mock_test.go -package=usecase
type authRepository interface {
	CreateRefreshSession(ctx context.Context, userID snow.ID, tokenHash string, startedAt, expiresAt time.Time) (int64, error)
	GetRefreshSession(ctx context.Context, id int64) (*domain.RefreshToken, error)
	GetLegacyRefreshToken(ctx context.Context, token string) (*domain.RefreshToken, error)
	RotateRefreshSession(ctx context.Context, rotation domain.RefreshRotation) (bool, error)
	DeleteRefreshSession(ctx context.Context, id int64) error
	CreateSSHLoginChallenge(ctx context.Context, email, nonce string, expiresAt time.Time) error
	DeleteExpiredSSHLoginChallenges(ctx context.Context, before time.Time) error
	ConsumeSSHLoginChallenge(ctx context.Context, nonce string) (*domain.SSHLoginChallenge, error)
}

type authSSHKeyRepository interface {
	GetByFingerprint(ctx context.Context, fingerprint string) (*domain.SSHPublicKey, error)
}

type passwordHasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) bool
}

type Auth struct {
	limiter        *ratelimit.Limiter
	secret         string
	passwordHasher passwordHasher
	userRepo       userRepository
	authRepo       authRepository
	sshKeyRepo     authSSHKeyRepository
	sshAudience    string
	audit          AuditRecorder
	now            func() time.Time
	dummyOnce      sync.Once
	dummyHash      string
}

func NewAuth(secret string, passwordHasher passwordHasher, userRepo userRepository, authRepo authRepository) *Auth {
	return &Auth{
		secret:         secret,
		passwordHasher: passwordHasher,
		userRepo:       userRepo,
		authRepo:       authRepo,
		now:            time.Now,
	}
}

func (a *Auth) WithAudit(rec AuditRecorder) *Auth {
	a.audit = rec
	return a
}

// WithClock replaces the clock used for refresh sessions.
func (a *Auth) WithClock(now func() time.Time) *Auth {
	a.now = now
	return a
}

func (a *Auth) WithLimiter(limiter *ratelimit.Limiter) *Auth {
	a.limiter = limiter
	return a
}

// throttleIdentity counts a login attempt per email and client IP, then
// against the email-wide ceiling. Without a client IP the per-attempt bucket
// is keyed on the email alone.
func (a *Auth) throttleIdentity(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if a.limiter == nil || email == "" {
		return nil
	}
	key := email
	if meta, ok := domain.RequestMetaFromContext(ctx); ok && meta.ClientIP != "" {
		key = email + "\x00" + meta.ClientIP
	}
	if err := allowLimit(ctx, a.limiter, ratelimit.ScopeAuthID, hashSubject(key)); err != nil {
		return err
	}
	return allowLimit(ctx, a.limiter, ratelimit.ScopeAuthEmail, hashSubject(email))
}

func hashSubject(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}

func allowLimit(ctx context.Context, limiter *ratelimit.Limiter, scope, subject string) error {
	res := limiter.Allow(ctx, scope, subject)
	if !res.Allowed {
		return domain.NewErrorTooManyRequests(res.RetryAfter)
	}
	return nil
}

func (a *Auth) WithSSHKeys(repo authSSHKeyRepository) *Auth {
	a.sshKeyRepo = repo
	return a
}

// WithSSHAudience sets the canonical origin SSH login signatures must be bound
// to; while it is empty SSH key login is refused.
func (a *Auth) WithSSHAudience(origin string) *Auth {
	a.sshAudience = origin
	return a
}

func (a *Auth) sshAudienceConfigured() error {
	if a.sshAudience == "" {
		return domain.NewErrorUser("SSH key login is not enabled on this server: set PUBLIC_BASE_URL")
	}
	return nil
}

func (a *Auth) LoginWithEmailPassword(ctx context.Context, email string, password string) (*dto.LoginResult, error) {
	if err := a.throttleIdentity(ctx, email); err != nil {
		return nil, err
	}
	user, err := a.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			a.spendPasswordCheck(password)
			a.recordLoginFailure(ctx, LoginMethodPassword, email, 0, "invalid_credentials")
			return nil, errInvalidPassword()
		}
		return nil, err
	}
	if user.Password == "" {
		a.spendPasswordCheck(password)
		a.recordLoginFailure(ctx, LoginMethodPassword, email, user.ID, "no_password")
		return nil, errInvalidPassword()
	}
	if !a.passwordHasher.Compare(user.Password, password) {
		a.recordLoginFailure(ctx, LoginMethodPassword, email, user.ID, "invalid_credentials")
		return nil, errInvalidPassword()
	}
	if !user.EmailVerified {
		a.recordLoginFailure(ctx, LoginMethodPassword, email, user.ID, "email_not_verified")
		return nil, errEmailNotVerified()
	}
	return a.LoginResultVia(ctx, user, LoginMethodPassword)
}

func errInvalidPassword() error {
	return domain.NewErrorUser("invalid email or password")
}

// spendPasswordCheck burns a hash comparison for an account without a usable
// password so the response time does not reveal whether the email is
// registered or how it signs in.
func (a *Auth) spendPasswordCheck(password string) {
	if a.passwordHasher == nil {
		return
	}
	a.dummyOnce.Do(func() {
		a.dummyHash, _ = a.passwordHasher.Hash("nipa-timing-equalizer")
	})
	a.passwordHasher.Compare(a.dummyHash, password)
}

// errEmailNotVerified refuses logins of accounts that have not confirmed their
// email yet; the SPA offers a resend link on this error.
func errEmailNotVerified() error {
	return domain.NewErrorForbidden("email not confirmed")
}

// BeginSSHKeyLogin issues a challenge for an SSH key login. The email is
// optional: when set, the completed login must present a key registered to
// that account; when empty, the key itself identifies the account.
func (a *Auth) BeginSSHKeyLogin(ctx context.Context, email string) (*dto.SSHKeyChallenge, error) {
	email = strings.TrimSpace(email)
	if err := a.sshAudienceConfigured(); err != nil {
		return nil, err
	}
	if err := a.throttleIdentity(ctx, email); err != nil {
		return nil, err
	}
	nonce, err := newSSHLoginNonce()
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	now := time.Now()
	if err := a.authRepo.DeleteExpiredSSHLoginChallenges(ctx, now); err != nil {
		return nil, err
	}
	if err := a.authRepo.CreateSSHLoginChallenge(ctx, email, nonce, now.Add(sshLoginChallengeTTL)); err != nil {
		return nil, err
	}
	return &dto.SSHKeyChallenge{
		Nonce:     nonce,
		Audience:  a.sshAudience,
		ExpiresIn: int(sshLoginChallengeTTL.Seconds()),
	}, nil
}

func (a *Auth) CompleteSSHKeyLogin(ctx context.Context, email, nonce string, publicKey, signature []byte) (*dto.LoginResult, error) {
	email = strings.TrimSpace(email)
	if err := a.sshAudienceConfigured(); err != nil {
		return nil, err
	}
	if err := a.throttleIdentity(ctx, email); err != nil {
		return nil, err
	}
	if nonce == "" {
		return nil, domain.NewErrorUser("nonce must not be empty")
	}
	if len(publicKey) == 0 || len(signature) == 0 {
		return nil, domain.NewErrorUser("public key and signature are required")
	}
	if a.sshKeyRepo == nil {
		return nil, sshLoginFailed()
	}
	attempt := &sshAttempt{}
	user, err := a.completeSSHKeyLogin(ctx, attempt, email, nonce, publicKey, signature)
	if err != nil {
		if attempt.reason != "" && a.audit != nil {
			target := attempt.userID
			if target == 0 && email != "" {
				if found, err := a.userRepo.GetByEmail(ctx, email); err == nil {
					target = found.ID
				}
			}
			a.recordLoginFailure(ctx, LoginMethodSSHKey, email, target, attempt.reason)
		}
		return nil, err
	}
	return a.LoginResultVia(ctx, user, LoginMethodSSHKey)
}

// sshAttempt carries why a well-formed SSH login was refused and which
// account it was aimed at, for the failed-login audit entry.
type sshAttempt struct {
	reason string
	userID snow.ID
}

func (s *sshAttempt) fail(reason string, userID snow.ID) error {
	s.reason = reason
	s.userID = userID
	return sshLoginFailed()
}

func (a *Auth) completeSSHKeyLogin(ctx context.Context, attempt *sshAttempt, email, nonce string, publicKey, signature []byte) (*domain.User, error) {
	challenge, err := a.authRepo.ConsumeSSHLoginChallenge(ctx, nonce)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, attempt.fail("invalid_challenge", 0)
		}
		return nil, err
	}
	if challenge.Email != email || challenge.ExpiresAt.Before(time.Now()) {
		return nil, attempt.fail("invalid_challenge", 0)
	}

	pub, err := ssh.ParsePublicKey(publicKey)
	if err != nil {
		return nil, attempt.fail("invalid_key", 0)
	}
	if err := sshkey.ValidateSupported(pub); err != nil {
		return nil, attempt.fail("invalid_key", 0)
	}

	key, err := a.sshKeyRepo.GetByFingerprint(ctx, ssh.FingerprintSHA256(pub))
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, attempt.fail("unknown_key", 0)
		}
		return nil, err
	}

	user, err := a.sshUser(ctx, email, key.UserID)
	if err != nil {
		if domain.IsServerError(err) {
			return nil, err
		}
		target := snow.ID(0)
		if email == "" {
			target = key.UserID
		}
		return nil, attempt.fail("key_mismatch", target)
	}

	if err := sshkey.Verify(pub, sshkey.LoginPayload(a.sshAudience, nonce), signature); err != nil {
		return nil, attempt.fail("invalid_signature", user.ID)
	}
	return user, nil
}

// sshUser resolves the account an SSH login belongs to: the email is an
// optional assertion, otherwise the registered key identifies its owner.
func (a *Auth) sshUser(ctx context.Context, email string, keyUserID snow.ID) (*domain.User, error) {
	if email == "" {
		user, err := a.userRepo.GetByID(ctx, keyUserID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, sshLoginFailed()
			}
			return nil, err
		}
		if !user.EmailVerified {
			return nil, sshLoginFailed()
		}
		return user, nil
	}
	user, err := a.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, sshLoginFailed()
		}
		return nil, err
	}
	if user.ID != keyUserID {
		return nil, sshLoginFailed()
	}
	if !user.EmailVerified {
		return nil, sshLoginFailed()
	}
	return user, nil
}

func sshLoginFailed() error {
	return domain.NewErrorUser("invalid email or SSH key")
}

func newSSHLoginNonce() (string, error) {
	buf := make([]byte, sshLoginChallengeBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *Auth) ValidateToken(ctx context.Context, tokenString string) (*domain.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &domain.Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, domain.NewErrorUser("unexpected signing method")
		}
		return []byte(a.secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, errInvalidToken()
	}
	claims, ok := token.Claims.(*domain.Claims)
	if !ok || !token.Valid {
		return nil, errInvalidToken()
	}
	user, err := a.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, errInvalidToken()
		}
		return nil, err
	}
	if user.TokenVersion != claims.TokenVersion {
		return nil, errInvalidToken()
	}
	claims.IsAdmin = user.IsAdmin
	claims.IsSuperAdmin = user.IsSuperAdmin
	return claims, nil
}

func errInvalidToken() error {
	return domain.NewErrorUnauthorized("invalid token")
}

// Login methods recorded on login audit entries.
const (
	LoginMethodPassword          = "password"
	LoginMethodSSHKey            = "ssh_key"
	LoginMethodOAuth             = "oauth"
	LoginMethodEmailVerification = "email_verification"
)

// LoginResultVia issues a session for a user who just signed in with method
// and records the login. SaaS flows (registration verification, OAuth
// completion) call it after establishing the account.
func (a *Auth) LoginResultVia(ctx context.Context, user *domain.User, method string) (*dto.LoginResult, error) {
	result, err := a.LoginResult(ctx, user)
	if err != nil {
		return nil, err
	}
	a.recordLogin(ctx, domain.AuditEntry{
		ActorType:   domain.AuditActorUser,
		ActorUserID: domain.IDPtr(user.ID),
		Action:      domain.AuditAuthLoginSucceeded,
		TargetType:  domain.AuditTargetUser,
		TargetID:    user.ID.Base36(),
		Metadata:    map[string]any{"method": method},
	})
	return result, nil
}

func (a *Auth) recordLoginFailure(ctx context.Context, method, email string, userID snow.ID, reason string) {
	entry := domain.AuditEntry{
		ActorType: domain.AuditActorSystem,
		Action:    domain.AuditAuthLoginFailed,
		Metadata: map[string]any{
			"method": method,
			"email":  normalizeEmail(email),
			"reason": reason,
		},
	}
	if userID != 0 {
		entry.TargetType = domain.AuditTargetUser
		entry.TargetID = userID.Base36()
	}
	a.recordLogin(ctx, entry)
}

// recordLogin writes a login audit entry best-effort: a failed record is
// logged and never changes the outcome of the login.
func (a *Auth) recordLogin(ctx context.Context, entry domain.AuditEntry) {
	if a.audit == nil {
		return
	}
	if err := a.audit.Record(context.WithoutCancel(ctx), entry); err != nil {
		obs.Log(ctx).Warn("audit login failed", "action", entry.Action, "target", entry.TargetID, "error", err)
	}
}

// LoginResult issues the access token and refresh token for an already
// authenticated user without recording a login; sign-in flows use
// LoginResultVia.
func (a *Auth) LoginResult(ctx context.Context, user *domain.User) (*dto.LoginResult, error) {
	now := a.now()
	secret, hash, err := newRefreshSecret()
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	id, err := a.authRepo.CreateRefreshSession(ctx, user.ID, hash, now, now.Add(refreshTokenExpiration))
	if err != nil {
		return nil, err
	}
	return a.loginResult(user, a.formatRefreshToken(id, secret))
}

func (a *Auth) loginResult(user *domain.User, refreshToken string) (*dto.LoginResult, error) {
	timeStart := time.Now()
	claims := domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.Base36(),
			IssuedAt:  jwt.NewNumericDate(timeStart),
			ExpiresAt: jwt.NewNumericDate(timeStart.Add(tokenExpirationMinutes * time.Minute)),
		},
		UserID:       user.ID,
		IsSuperAdmin: user.IsSuperAdmin,
		IsAdmin:      user.IsAdmin,
		OrgID:        []snow.ID{},
		TokenVersion: user.TokenVersion,
	}
	jwt, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.secret))
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}

	return &dto.LoginResult{
		AccessToken:      jwt,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        tokenExpirationMinutes * 60,
		RefreshExpiresIn: int(refreshTokenExpiration.Seconds()),
	}, nil
}
