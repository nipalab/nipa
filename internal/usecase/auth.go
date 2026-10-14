package usecase

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/nipalab/nipa/internal/domain"
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
	SaveRefreshToken(ctx context.Context, userID snow.ID, refreshToken string, expiresAt time.Time) error
	GetAndDeleteRefreshToken(ctx context.Context, refreshToken string) (*domain.RefreshToken, error)
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
	secret         string
	passwordHasher passwordHasher
	userRepo       userRepository
	authRepo       authRepository
	sshKeyRepo     authSSHKeyRepository
}

func NewAuth(secret string, passwordHasher passwordHasher, userRepo userRepository, authRepo authRepository) *Auth {
	return &Auth{
		secret:         secret,
		passwordHasher: passwordHasher,
		userRepo:       userRepo,
		authRepo:       authRepo,
	}
}

// WithSSHKeys enables SSH key login on this usecase.
func (a *Auth) WithSSHKeys(repo authSSHKeyRepository) *Auth {
	a.sshKeyRepo = repo
	return a
}

func (a *Auth) LoginWithEmailPassword(ctx context.Context, email string, password string) (*dto.LoginResult, error) {
	user, err := a.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorUser("invalid email or password")
		}
		return nil, err
	}
	if !a.passwordHasher.Compare(user.Password, password) {
		return nil, domain.NewErrorUser("invalid email or password")
	}
	return a.generateLoginResult(ctx, user)
}

func (a *Auth) LoginWithRefreshToken(ctx context.Context, refreshToken string) (*dto.LoginResult, error) {
	storedToken, err := a.authRepo.GetAndDeleteRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	if storedToken.ExpiresAt.Before(time.Now()) {
		return nil, domain.NewErrorUser("refresh token expired")
	}

	user, err := a.userRepo.GetByID(ctx, storedToken.UserID)
	if err != nil {
		return nil, err
	}

	return a.generateLoginResult(ctx, user)
}

// BeginSSHKeyLogin issues a challenge for an SSH key login. The email is
// optional: when set, the completed login must present a key registered to
// that account; when empty, the key itself identifies the account.
func (a *Auth) BeginSSHKeyLogin(ctx context.Context, email string) (*dto.SSHKeyChallenge, error) {
	email = strings.TrimSpace(email)
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
		ExpiresIn: int(sshLoginChallengeTTL.Seconds()),
	}, nil
}

func (a *Auth) CompleteSSHKeyLogin(ctx context.Context, email, nonce string, publicKey, signature []byte) (*dto.LoginResult, error) {
	email = strings.TrimSpace(email)
	if nonce == "" {
		return nil, domain.NewErrorUser("nonce must not be empty")
	}
	if len(publicKey) == 0 || len(signature) == 0 {
		return nil, domain.NewErrorUser("public key and signature are required")
	}
	if a.sshKeyRepo == nil {
		return nil, sshLoginFailed()
	}

	challenge, err := a.authRepo.ConsumeSSHLoginChallenge(ctx, nonce)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, sshLoginFailed()
		}
		return nil, err
	}
	if challenge.Email != email || challenge.ExpiresAt.Before(time.Now()) {
		return nil, sshLoginFailed()
	}

	pub, err := ssh.ParsePublicKey(publicKey)
	if err != nil {
		return nil, sshLoginFailed()
	}
	if err := sshkey.ValidateSupported(pub); err != nil {
		return nil, sshLoginFailed()
	}

	key, err := a.sshKeyRepo.GetByFingerprint(ctx, ssh.FingerprintSHA256(pub))
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, sshLoginFailed()
		}
		return nil, err
	}

	user, err := a.sshUser(ctx, email, key.UserID)
	if err != nil {
		return nil, err
	}

	if err := sshkey.Verify(pub, sshkey.LoginPayload(nonce), signature); err != nil {
		return nil, sshLoginFailed()
	}
	return a.generateLoginResult(ctx, user)
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

func (a *Auth) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	if _, err := a.authRepo.GetAndDeleteRefreshToken(ctx, refreshToken); err != nil {
		if domain.IsErrorNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func (a *Auth) ValidateToken(ctx context.Context, tokenString string) (*domain.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &domain.Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, domain.NewErrorUser("unexpected signing method")
		}
		return []byte(a.secret), nil
	})

	if err != nil {
		return nil, domain.NewErrorUser("invalid token")
	}

	if claims, ok := token.Claims.(*domain.Claims); ok && token.Valid {
		return claims, nil
	} else {
		return nil, domain.NewErrorUser("invalid token")
	}
}

func (a *Auth) generateLoginResult(ctx context.Context, user *domain.User) (*dto.LoginResult, error) {
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
	}
	jwt, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.secret))
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}

	refreshToken := uuid.New().String()
	tokenExpiresAt := timeStart.Add(refreshTokenExpiration)
	err = a.authRepo.SaveRefreshToken(ctx, user.ID, refreshToken, tokenExpiresAt)
	if err != nil {
		return nil, err
	}

	return &dto.LoginResult{
		AccessToken:      jwt,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        tokenExpirationMinutes * 60,
		RefreshExpiresIn: int(refreshTokenExpiration.Seconds()),
	}, nil
}
