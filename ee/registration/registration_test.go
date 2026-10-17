package registration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubTransactor struct{}

func (stubTransactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

type stubHasher struct{}

func (stubHasher) Hash(password string) (string, error) { return "hash:" + password, nil }
func (stubHasher) Compare(hash, password string) bool   { return hash == "hash:"+password }

type stubUsers struct {
	users []*domain.User
}

func (s *stubUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, user := range s.users {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (s *stubUsers) GetByID(_ context.Context, id snow.ID) (*domain.User, error) {
	for _, user := range s.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, domain.NewErrorRecordNotFound()
}

type verificationRow struct {
	userID    snow.ID
	tokenHash string
	expiresAt time.Time
}

type stubRepo struct {
	created            []domain.User
	verifications      map[snow.ID]*verificationRow
	verified           []snow.ID
	quotas             map[snow.ID]int64
	expiredSweeps      int
	deletedVerfication []snow.ID
	createErr          error
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		verifications: map[snow.ID]*verificationRow{},
		quotas:        map[snow.ID]int64{},
	}
}

func (s *stubRepo) CreateUnverifiedUser(_ context.Context, user domain.User) (*domain.User, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	user.EmailVerified = false
	s.created = append(s.created, user)
	userCopy := user
	return &userCopy, nil
}

func (s *stubRepo) UpsertEmailVerification(_ context.Context, userID snow.ID, tokenHash string, expiresAt time.Time) error {
	s.verifications[userID] = &verificationRow{userID: userID, tokenHash: tokenHash, expiresAt: expiresAt}
	return nil
}

func (s *stubRepo) EmailVerificationByToken(_ context.Context, tokenHash string) (snow.ID, time.Time, error) {
	for _, row := range s.verifications {
		if row.tokenHash == tokenHash {
			return row.userID, row.expiresAt, nil
		}
	}
	return 0, time.Time{}, domain.NewErrorRecordNotFound()
}

func (s *stubRepo) DeleteEmailVerification(_ context.Context, userID snow.ID) error {
	s.deletedVerfication = append(s.deletedVerfication, userID)
	delete(s.verifications, userID)
	return nil
}

func (s *stubRepo) DeleteExpiredEmailVerifications(_ context.Context, _ time.Time) error {
	s.expiredSweeps++
	return nil
}

func (s *stubRepo) SetEmailVerified(_ context.Context, userID snow.ID) error {
	s.verified = append(s.verified, userID)
	return nil
}

func (s *stubRepo) SeedOwnerQuota(_ context.Context, userID snow.ID, quotaBytes int64) error {
	s.quotas[userID] = quotaBytes
	return nil
}

type stubOutbox struct {
	deliveries []*domain.EmailDelivery
}

func (s *stubOutbox) Enqueue(_ context.Context, deliveries []*domain.EmailDelivery) error {
	s.deliveries = append(s.deliveries, deliveries...)
	return nil
}

type stubKicker struct {
	kicks int
}

func (s *stubKicker) Kick() { s.kicks++ }

type registrationEnv struct {
	registration *Registration
	users        *stubUsers
	repo         *stubRepo
	outbox       *stubOutbox
	kicker       *stubKicker
	now          time.Time
}

func newRegistrationEnv(t *testing.T) *registrationEnv {
	t.Helper()

	node, err := snow.NewNode(1)
	require.NoError(t, err)

	env := &registrationEnv{
		users:  &stubUsers{},
		repo:   newStubRepo(),
		outbox: &stubOutbox{},
		kicker: &stubKicker{},
		now:    time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}
	env.registration = New(node, stubHasher{}, env.users, env.repo, stubTransactor{}, Config{
		FreeQuotaBytes: 15 * 1024 * 1024 * 1024,
		BaseURL:        "https://nipa.example.com",
	}).WithMailer(env.outbox, env.kicker)
	env.registration.now = func() time.Time { return env.now }
	return env
}

func TestRegisterCreatesInactiveAccountAndQueuesEmail(t *testing.T) {
	env := newRegistrationEnv(t)

	err := env.registration.Register(context.Background(), " Alice ", " alice@example.com ", "password123")
	require.NoError(t, err)

	require.Len(t, env.repo.created, 1)
	created := env.repo.created[0]
	require.Equal(t, "Alice", created.Name)
	require.Equal(t, "alice@example.com", created.Email)
	require.Equal(t, "hash:password123", created.Password)
	require.NotZero(t, created.ID)

	row, ok := env.repo.verifications[created.ID]
	require.True(t, ok)
	require.NotEmpty(t, row.tokenHash)
	require.WithinDuration(t, env.now.Add(72*time.Hour), row.expiresAt, time.Second)
	require.Equal(t, 1, env.repo.expiredSweeps)

	require.Equal(t, int64(15*1024*1024*1024), env.repo.quotas[created.ID])

	require.Len(t, env.outbox.deliveries, 1)
	delivery := env.outbox.deliveries[0]
	require.Equal(t, verificationEvent, delivery.Event)
	require.Equal(t, created.ID, delivery.UserID)
	require.Equal(t, "alice@example.com", delivery.Email)
	require.Equal(t, "Confirm your email address", delivery.Subject)
	require.Equal(t, domain.EmailDeliveryPending, delivery.State)
	require.NotEmpty(t, delivery.Body)
	require.Equal(t, 1, env.kicker.kicks)
}

func TestRegisterDuplicateEmail(t *testing.T) {
	env := newRegistrationEnv(t)
	env.users.users = []*domain.User{{ID: 7, Email: "alice@example.com"}}

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", "password123")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Empty(t, env.repo.created)
}

func TestRegisterRejectsWeakPasswordAndInvalidIdentity(t *testing.T) {
	env := newRegistrationEnv(t)

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", "short")
	require.Error(t, err)

	err = env.registration.Register(context.Background(), "", "alice@example.com", "password123")
	require.Error(t, err)

	err = env.registration.Register(context.Background(), "Alice", "not-an-email", "password123")
	require.Error(t, err)
	require.Empty(t, env.repo.created)
}

func TestRegisterWithoutMailerStillCreatesTheAccount(t *testing.T) {
	env := newRegistrationEnv(t)
	env.registration.outbox = nil
	env.registration.kick = nil

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", "password123")
	require.NoError(t, err)
	require.Len(t, env.repo.created, 1)
	require.NotEmpty(t, env.repo.verifications)
}

func TestVerifyActivatesAccount(t *testing.T) {
	env := newRegistrationEnv(t)
	user := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: false}
	env.users.users = []*domain.User{user}
	token := "raw-token"
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken(token), expiresAt: env.now.Add(time.Hour)}

	verified, err := env.registration.Verify(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, snow.ID(42), verified.ID)
	require.Equal(t, []snow.ID{42}, env.repo.verified)
	require.Contains(t, env.repo.deletedVerfication, snow.ID(42))
	require.NotContains(t, env.repo.verifications, snow.ID(42))
}

func TestVerifyRejectsUnknownToken(t *testing.T) {
	env := newRegistrationEnv(t)

	_, err := env.registration.Verify(context.Background(), "nope")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	env := newRegistrationEnv(t)
	token := "raw-token"
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken(token), expiresAt: env.now.Add(-time.Minute)}

	_, err := env.registration.Verify(context.Background(), token)
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Empty(t, env.repo.verified)
	require.NotContains(t, env.repo.verifications, snow.ID(42))
}

func TestResendReissuesTokenForUnconfirmedAccount(t *testing.T) {
	env := newRegistrationEnv(t)
	user := &domain.User{ID: 42, Name: "Alice", Email: "alice@example.com", EmailVerified: false}
	env.users.users = []*domain.User{user}
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: "old-hash", expiresAt: env.now.Add(time.Hour)}

	err := env.registration.ResendVerification(context.Background(), "alice@example.com")
	require.NoError(t, err)
	require.NotEqual(t, "old-hash", env.repo.verifications[42].tokenHash)
	require.Len(t, env.outbox.deliveries, 1)
	require.Equal(t, "alice@example.com", env.outbox.deliveries[0].Email)
	require.Equal(t, 1, env.kicker.kicks)
}

func TestResendIsSilentForUnknownAndConfirmedAccounts(t *testing.T) {
	env := newRegistrationEnv(t)

	err := env.registration.ResendVerification(context.Background(), "nobody@example.com")
	require.NoError(t, err)

	env.users.users = []*domain.User{{ID: 7, Email: "alice@example.com", EmailVerified: true}}
	err = env.registration.ResendVerification(context.Background(), "alice@example.com")
	require.NoError(t, err)
	require.Empty(t, env.outbox.deliveries)
}

func TestVerificationURLUsesTheBaseURL(t *testing.T) {
	env := newRegistrationEnv(t)

	link := env.registration.verificationURL("tok en")
	require.Equal(t, "https://nipa.example.com/verify?token=tok+en", link)
	require.True(t, strings.HasPrefix(link, "https://nipa.example.com/verify?token="))
}

func TestMessageHostFallsBackWithoutABaseURL(t *testing.T) {
	env := newRegistrationEnv(t)

	require.Equal(t, "nipa.example.com", env.registration.messageHost())
	env.registration.cfg.BaseURL = ""
	require.Equal(t, "nipa.local", env.registration.messageHost())
	env.registration.cfg.BaseURL = "://invalid"
	require.Equal(t, "nipa.local", env.registration.messageHost())
}
