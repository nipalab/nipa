package usecase

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/usecase/dto"
)

const (
	refreshGraceWindow   = 30 * time.Second
	refreshSessionMaxAge = 90 * 24 * time.Hour
	refreshSecretBytes   = 32
	refreshTagBytes      = 16

	refreshReuseUnknownSecret = "unknown_secret"
	refreshReuseAfterGrace    = "previous_secret_after_grace"
)

// LoginWithRefreshToken rotates a refresh session in place. A token that was
// just rotated by a concurrent request answers "not found"; any other stale or
// unknown secret for an existing session revokes it.
func (a *Auth) LoginWithRefreshToken(ctx context.Context, refreshToken string) (*dto.LoginResult, error) {
	id, secret, ok := a.parseRefreshToken(refreshToken)
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	session, err := a.authRepo.GetRefreshSession(ctx, id)
	if err != nil {
		return nil, err
	}
	now := a.now()
	hash := hashRefreshSecret(secret)
	if !hashEqual(hash, session.TokenHash) {
		if hashEqual(hash, session.PreviousHash) {
			if session.RotatedAt != nil && now.Sub(*session.RotatedAt) <= refreshGraceWindow {
				return nil, domain.NewErrorRecordNotFound()
			}
			return nil, a.revokeReusedSession(ctx, session, refreshReuseAfterGrace)
		}
		return nil, a.revokeReusedSession(ctx, session, refreshReuseUnknownSecret)
	}
	started := session.CreatedAt
	if session.SessionStartedAt != nil {
		started = *session.SessionStartedAt
	}
	return a.rotateRefreshSession(ctx, session, started, domain.RefreshRotation{ID: id, MatchHash: session.TokenHash}, now)
}

func (a *Auth) rotateRefreshSession(ctx context.Context, session *domain.RefreshToken, started time.Time, rotation domain.RefreshRotation, now time.Time) (*dto.LoginResult, error) {
	if session.ExpiresAt.Before(now) || now.Sub(started) > refreshSessionMaxAge {
		if err := a.authRepo.DeleteRefreshSession(ctx, session.ID); err != nil {
			return nil, err
		}
		return nil, domain.NewErrorUser("refresh token expired")
	}
	user, err := a.userRepo.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerified {
		return nil, errEmailNotVerified()
	}
	secret, hash, err := newRefreshSecret()
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	rotation.NewHash = hash
	rotation.RotatedAt = now
	rotation.ExpiresAt = now.Add(refreshTokenExpiration)
	rotated, err := a.authRepo.RotateRefreshSession(ctx, rotation)
	if err != nil {
		return nil, err
	}
	if !rotated {
		return nil, domain.NewErrorRecordNotFound()
	}
	return a.loginResult(user, a.formatRefreshToken(session.ID, secret), sessionAuth{
		method:  session.AuthMethod,
		mfa:     session.MFA,
		started: started,
	})
}

// revokeReusedSession deletes a session whose stale secret was replayed. The
// deletion and its audit entry commit before the caller sees the error.
func (a *Auth) revokeReusedSession(ctx context.Context, session *domain.RefreshToken, reason string) error {
	err := auditedWrite(ctx, a.audit, func(ctx context.Context) error {
		return a.authRepo.DeleteRefreshSession(ctx, session.ID)
	}, func() *domain.AuditEntry {
		return &domain.AuditEntry{
			ActorType:  domain.AuditActorSystem,
			Action:     domain.AuditAuthRefreshTokenReuseDetected,
			TargetType: domain.AuditTargetUser,
			TargetID:   session.UserID.Base36(),
			Metadata: map[string]any{
				"session_id": strconv.FormatInt(session.ID, 36),
				"reason":     reason,
			},
		}
	})
	if err != nil {
		return err
	}
	return domain.NewErrorUnauthorized("refresh token revoked")
}

// Logout ends the session the token names. A session token must carry its
// current or previous secret, so a session id alone cannot end it.
func (a *Auth) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	id, secret, ok := a.parseRefreshToken(refreshToken)
	if !ok {
		return nil
	}
	session, err := a.authRepo.GetRefreshSession(ctx, id)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil
		}
		return err
	}
	hash := hashRefreshSecret(secret)
	if !hashEqual(hash, session.TokenHash) && !hashEqual(hash, session.PreviousHash) {
		return nil
	}
	return a.authRepo.DeleteRefreshSession(ctx, session.ID)
}

func newRefreshSecret() (string, string, error) {
	buf := make([]byte, refreshSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(buf)
	return secret, hashRefreshSecret(secret), nil
}

func hashRefreshSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func hashEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// The wire session id is the row id plus an HMAC tag, so a stale secret can
// only be presented by someone who once held a token of that session.
func (a *Auth) sessionTag(id36 string) string {
	mac := hmac.New(sha256.New, []byte(a.secret))
	mac.Write([]byte("nipa-refresh-session:" + id36))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:refreshTagBytes])
}

func (a *Auth) formatRefreshToken(id int64, secret string) string {
	id36 := strconv.FormatInt(id, 36)
	return id36 + "." + a.sessionTag(id36) + "." + secret
}

// parseRefreshToken reports ok=false for anything that is not a session token
// carrying a valid tag.
func (a *Auth) parseRefreshToken(token string) (int64, string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 36, 64)
	if err != nil || id <= 0 || parts[2] == "" || !hmac.Equal([]byte(parts[1]), []byte(a.sessionTag(parts[0]))) {
		return 0, "", false
	}
	return id, parts[2], true
}
