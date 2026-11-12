package registration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
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
	err   error
}

func (s *stubUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	if s.err != nil {
		return nil, s.err
	}
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
	passwordErr        error
	passwords          map[snow.ID]string
	terms              []domain.TermsAcceptance
	termsErr           error
	latestTermsErr     error
	sweepErr           error
}

func (s *stubRepo) RecordTermsAcceptance(_ context.Context, acceptance domain.TermsAcceptance) error {
	if s.termsErr != nil {
		return s.termsErr
	}
	s.terms = append(s.terms, acceptance)
	return nil
}

func (s *stubRepo) LatestTermsAcceptance(_ context.Context, userID snow.ID) (*domain.TermsAcceptance, error) {
	if s.latestTermsErr != nil {
		return nil, s.latestTermsErr
	}
	for i := len(s.terms) - 1; i >= 0; i-- {
		if s.terms[i].UserID == userID {
			acceptance := s.terms[i]
			return &acceptance, nil
		}
	}
	return nil, domain.NewErrorRecordNotFound()
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
	if s.sweepErr != nil {
		return s.sweepErr
	}
	s.expiredSweeps++
	return nil
}

func (s *stubRepo) SetEmailVerified(_ context.Context, userID snow.ID) error {
	s.verified = append(s.verified, userID)
	return nil
}

func (s *stubRepo) SetPasswordHash(_ context.Context, userID snow.ID, passwordHash string) error {
	if s.passwordErr != nil {
		return s.passwordErr
	}
	if s.passwords == nil {
		s.passwords = map[snow.ID]string{}
	}
	s.passwords[userID] = passwordHash
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

	err := env.registration.Register(context.Background(), " Alice ", " alice@example.com ", false)
	require.NoError(t, err)

	require.Len(t, env.repo.created, 1)
	created := env.repo.created[0]
	require.Equal(t, "Alice", created.Name)
	require.Equal(t, "alice@example.com", created.Email)
	require.Empty(t, created.Password, "the password is chosen at confirmation, not at sign-up")
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

func TestRegisterConfirmedEmailLooksLikeASuccess(t *testing.T) {
	env := newRegistrationEnv(t)
	env.users.users = []*domain.User{{ID: 7, Email: "alice@example.com", EmailVerified: true}}

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", false)
	require.NoError(t, err, "the response must not reveal that the address is registered")
	require.Empty(t, env.repo.created)
	require.Empty(t, env.repo.passwords)
	require.Empty(t, env.repo.verifications)

	require.Equal(t, 1, env.repo.expiredSweeps, "the confirmed path runs the transaction a signup runs")
	require.Len(t, env.outbox.deliveries, 1, "the owner gets a notice instead of a confirmation link")
	delivery := env.outbox.deliveries[0]
	require.Equal(t, existingAccountEvent, delivery.Event)
	require.Equal(t, snow.ID(7), delivery.UserID)
	require.Equal(t, "You already have a Nipa account", delivery.Subject)
	require.Contains(t, string(delivery.Body), "https://nipa.example.com/login")
	require.NotContains(t, string(delivery.Body), "/verify?token=")
	require.Equal(t, 1, env.kicker.kicks)
}

func TestRegisterConfirmedEmailWithoutMailer(t *testing.T) {
	env := newRegistrationEnv(t)
	env.registration.outbox = nil
	env.users.users = []*domain.User{{ID: 7, Email: "alice@example.com", EmailVerified: true}}

	require.NoError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", false))
	require.Equal(t, 1, env.repo.expiredSweeps)
	require.Empty(t, env.outbox.deliveries)
}

func TestRegisterConfirmedEmailSweepError(t *testing.T) {
	env := newRegistrationEnv(t)
	env.repo.sweepErr = errors.New("db")
	env.users.users = []*domain.User{{ID: 7, Email: "alice@example.com", EmailVerified: true}}

	require.EqualError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", false), "db")
	require.Empty(t, env.outbox.deliveries)
	require.Zero(t, env.kicker.kicks)
}

func TestRegisterAndResendThrottledPerEmail(t *testing.T) {
	env := newRegistrationEnv(t)
	env.registration.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
		ratelimit.ScopeRegisterEmail: {Rate: 0.0001, Burst: 2},
	}))
	ctx := context.Background()

	require.NoError(t, env.registration.Register(ctx, "Alice", "alice@example.com", false))
	require.Len(t, env.repo.created, 1)
	env.users.users = []*domain.User{{ID: env.repo.created[0].ID, Name: "Alice", Email: "alice@example.com"}}
	require.NoError(t, env.registration.ResendVerification(ctx, "alice@example.com"))
	require.Len(t, env.outbox.deliveries, 2)

	requireTooManyRequests(t, env.registration.ResendVerification(ctx, " ALICE@example.com "))
	requireTooManyRequests(t, env.registration.Register(ctx, "Alice", "Alice@Example.com", false))
	requireTooManyRequests(t, env.registration.Register(ctx, "Nobody", "alice@example.com", false))
	require.Len(t, env.outbox.deliveries, 2, "nothing is sent while throttled")
	require.Len(t, env.repo.created, 1)

	require.NoError(t, env.registration.Register(ctx, "Bob", "bob@example.com", false))
	require.Len(t, env.repo.created, 2, "other addresses keep their own budget")
	require.Len(t, env.outbox.deliveries, 3)

	require.NoError(t, env.registration.ResendVerification(ctx, "nobody@example.com"))
	require.NoError(t, env.registration.ResendVerification(ctx, "nobody@example.com"))
	requireTooManyRequests(t, env.registration.ResendVerification(ctx, "nobody@example.com"))
}

func TestRegisterAgainReissuesTheTokenWithoutTouchingThePassword(t *testing.T) {
	env := newRegistrationEnv(t)
	env.users.users = []*domain.User{{ID: 7, Name: "Alice", Email: "alice@example.com", EmailVerified: false}}
	env.repo.verifications[7] = &verificationRow{userID: 7, tokenHash: "earlier-token", expiresAt: env.now.Add(time.Hour)}

	err := env.registration.Register(context.Background(), "Mallory", "alice@example.com", false)
	require.NoError(t, err)
	require.Empty(t, env.repo.created, "no second account is created")
	require.Empty(t, env.repo.passwords, "re-registering can never set a password")
	require.NotEqual(t, "earlier-token", env.repo.verifications[7].tokenHash)
	require.Len(t, env.outbox.deliveries, 1)
	require.Equal(t, 1, env.kicker.kicks)
}

func TestRegisterLookupError(t *testing.T) {
	env := newRegistrationEnv(t)
	env.users.err = errors.New("db down")

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", false)
	require.EqualError(t, err, "db down")
}

func TestRegisterRejectsInvalidIdentity(t *testing.T) {
	env := newRegistrationEnv(t)

	require.Error(t, env.registration.Register(context.Background(), "", "alice@example.com", false))
	require.Error(t, env.registration.Register(context.Background(), "Alice", "not-an-email", false))
	require.Empty(t, env.repo.created)
}

func TestRegisterWithoutMailerStillCreatesTheAccount(t *testing.T) {
	env := newRegistrationEnv(t)
	env.registration.outbox = nil
	env.registration.kick = nil

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", false)
	require.NoError(t, err)
	require.Len(t, env.repo.created, 1)
	require.NotEmpty(t, env.repo.verifications)
}

func TestVerifySetsThePasswordAndActivatesTheAccount(t *testing.T) {
	env := newRegistrationEnv(t)
	user := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: false}
	env.users.users = []*domain.User{user}
	token := "raw-token"
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken(token), expiresAt: env.now.Add(time.Hour)}

	verified, err := env.registration.Verify(context.Background(), token, "chosen-password")
	require.NoError(t, err)
	require.Equal(t, snow.ID(42), verified.ID)
	require.Equal(t, "hash:chosen-password", env.repo.passwords[42], "the mailbox owner picks the password")
	require.Equal(t, []snow.ID{42}, env.repo.verified)
	require.Contains(t, env.repo.deletedVerfication, snow.ID(42))
	require.NotContains(t, env.repo.verifications, snow.ID(42))
}

func TestVerifyWithAWeakPasswordKeepsTheToken(t *testing.T) {
	env := newRegistrationEnv(t)
	token := "raw-token"
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken(token), expiresAt: env.now.Add(time.Hour)}

	_, err := env.registration.Verify(context.Background(), token, "short")
	require.Error(t, err)
	require.Contains(t, env.repo.verifications, snow.ID(42), "the link still works with a better password")
	require.Empty(t, env.repo.verified)
	require.Empty(t, env.repo.passwords)
}

func TestVerifyRequiresAToken(t *testing.T) {
	env := newRegistrationEnv(t)

	_, err := env.registration.Verify(context.Background(), "  ", "chosen-password")
	require.Error(t, err)
}

func TestVerifyRejectsUnknownToken(t *testing.T) {
	env := newRegistrationEnv(t)

	_, err := env.registration.Verify(context.Background(), "nope", "chosen-password")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	env := newRegistrationEnv(t)
	token := "raw-token"
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken(token), expiresAt: env.now.Add(-time.Minute)}

	_, err := env.registration.Verify(context.Background(), token, "chosen-password")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Empty(t, env.repo.verified)
	require.NotContains(t, env.repo.verifications, snow.ID(42))
}

func TestVerifyPasswordWriteError(t *testing.T) {
	env := newRegistrationEnv(t)
	env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com"}}
	token := "raw-token"
	env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken(token), expiresAt: env.now.Add(time.Hour)}
	env.repo.passwordErr = errors.New("boom")

	_, err := env.registration.Verify(context.Background(), token, "chosen-password")
	require.EqualError(t, err, "boom")
	require.Empty(t, env.repo.verified, "an account is never activated without its password")
}

func TestAttackerReRegisteringCannotChooseThePasswordTheVictimConfirmsWith(t *testing.T) {
	env := newRegistrationEnv(t)
	ctx := context.Background()
	require.NoError(t, env.registration.Register(ctx, "Victim", "victim@example.com", false))
	victim := env.repo.created[0]
	env.users.users = append(env.users.users, &victim)

	require.NoError(t, env.registration.Register(ctx, "Attacker", "victim@example.com", false))
	require.Empty(t, env.repo.passwords, "the attacker has no way to attach a password")
	require.Len(t, env.outbox.deliveries, 2)

	token := tokenFromBody(t, string(env.outbox.deliveries[1].Body))
	verified, err := env.registration.Verify(ctx, token, "the-victims-own-password")
	require.NoError(t, err)
	require.Equal(t, "hash:the-victims-own-password", env.repo.passwords[verified.ID])
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

func tokenFromBody(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, "token=")
	require.True(t, ok, body)
	end := strings.IndexAny(rest, "\\\n\"<& ")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func TestRegisterCreateError(t *testing.T) {
	env := newRegistrationEnv(t)
	env.repo.createErr = errors.New("insert failed")

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", false)
	require.EqualError(t, err, "insert failed")
	require.Empty(t, env.outbox.deliveries)
	require.Zero(t, env.kicker.kicks)
}

type recordedAudit struct {
	entries []domain.AuditEntry
	err     error
}

func (r *recordedAudit) Record(_ context.Context, entry domain.AuditEntry) error {
	if r.err != nil {
		return r.err
	}
	r.entries = append(r.entries, entry)
	return nil
}

func TestRegisterRecordsASystemAudit(t *testing.T) {
	env := newRegistrationEnv(t)
	rec := &recordedAudit{}
	env.registration.WithAudit(rec)

	require.NoError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", false))
	require.Len(t, rec.entries, 1)
	entry := rec.entries[0]
	require.Equal(t, domain.AuditUserCreated, entry.Action)
	require.Equal(t, domain.AuditActorSystem, entry.ActorType)
	require.Equal(t, "registration", entry.Metadata["source"])
	require.Equal(t, env.repo.created[0].ID.Base36(), entry.TargetID)

	failing := newRegistrationEnv(t)
	failing.registration.WithAudit(&recordedAudit{err: errors.New("audit down")})
	require.EqualError(t, failing.registration.Register(context.Background(), "Bob", "bob@example.com", false), "audit down")
	require.Empty(t, failing.outbox.deliveries)
}

func requireTooManyRequests(t *testing.T, err error) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 429, domErr.Code)
}
