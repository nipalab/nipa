package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeRefreshRepo struct {
	mu        sync.Mutex
	nextID    int64
	rows      map[int64]*domain.RefreshToken
	getErr    error
	createErr error
	rotateErr error
	deleteErr error
	beforeRot func()
}

func newFakeRefreshRepo() *fakeRefreshRepo {
	return &fakeRefreshRepo{rows: map[int64]*domain.RefreshToken{}}
}

func (f *fakeRefreshRepo) CreateRefreshSession(_ context.Context, session domain.NewRefreshSession) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	started := session.StartedAt
	f.rows[f.nextID] = &domain.RefreshToken{ID: f.nextID, UserID: session.UserID, TokenHash: session.TokenHash, SessionStartedAt: &started, ExpiresAt: session.ExpiresAt, CreatedAt: started, AuthMethod: session.AuthMethod, MFA: session.MFA}
	return f.nextID, nil
}

func (f *fakeRefreshRepo) GetRefreshSession(_ context.Context, id int64) (*domain.RefreshToken, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	cp := *row
	return &cp, nil
}

func (f *fakeRefreshRepo) RotateRefreshSession(_ context.Context, r domain.RefreshRotation) (bool, error) {
	if f.beforeRot != nil {
		f.beforeRot()
	}
	if f.rotateErr != nil {
		return false, f.rotateErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[r.ID]
	if !ok {
		return false, nil
	}
	rotatedAt := r.RotatedAt
	if row.TokenHash != r.MatchHash {
		return false, nil
	}
	row.PreviousHash = row.TokenHash
	row.TokenHash = r.NewHash
	row.RotatedAt = &rotatedAt
	row.ExpiresAt = r.ExpiresAt
	return true, nil
}

func (f *fakeRefreshRepo) DeleteRefreshSession(_ context.Context, id int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	return nil
}

func (f *fakeRefreshRepo) has(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.rows[id]
	return ok
}

func (f *fakeRefreshRepo) CreateSSHLoginChallenge(context.Context, string, string, time.Time) error {
	return nil
}

func (f *fakeRefreshRepo) DeleteExpiredSSHLoginChallenges(context.Context, time.Time) error {
	return nil
}

func (f *fakeRefreshRepo) ConsumeSSHLoginChallenge(context.Context, string) (*domain.SSHLoginChallenge, error) {
	return nil, domain.NewErrorRecordNotFound()
}

type fakeRefreshUsers struct {
	user *domain.User
	err  error
}

func (f fakeRefreshUsers) GetByEmail(context.Context, string) (*domain.User, error) {
	return f.user, f.err
}

func (f fakeRefreshUsers) GetByID(context.Context, snow.ID) (*domain.User, error) {
	return f.user, f.err
}

type txAuditRecorder struct {
	entries   []domain.AuditEntry
	txErrs    []error
	recordErr error
}

func (r *txAuditRecorder) Record(_ context.Context, entry domain.AuditEntry) error {
	if r.recordErr != nil {
		return r.recordErr
	}
	r.entries = append(r.entries, entry)
	return nil
}

func (r *txAuditRecorder) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	err := fn(ctx)
	r.txErrs = append(r.txErrs, err)
	return err
}

type refreshClock struct{ now time.Time }

func (c *refreshClock) Now() time.Time { return c.now }

type refreshHarness struct {
	auth  *Auth
	repo  *fakeRefreshRepo
	audit *txAuditRecorder
	clock *refreshClock
	user  *domain.User
}

func newRefreshHarness(t *testing.T) *refreshHarness {
	t.Helper()
	h := &refreshHarness{
		repo:  newFakeRefreshRepo(),
		audit: &txAuditRecorder{},
		clock: &refreshClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
		user:  &domain.User{ID: snow.ID(7), EmailVerified: true},
	}
	h.auth = NewAuth("secret", nil, fakeRefreshUsers{user: h.user}, h.repo).WithAudit(h.audit).WithClock(h.clock.Now)
	return h
}

func (h *refreshHarness) login(t *testing.T) (string, int64) {
	t.Helper()
	got, err := h.auth.LoginResult(context.Background(), h.user)
	require.NoError(t, err)
	id, _, ok := h.auth.parseRefreshToken(got.RefreshToken)
	require.True(t, ok)
	require.NotZero(t, id)
	return got.RefreshToken, id
}

func requireStatus(t *testing.T, err error, code int) {
	t.Helper()
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Equal(t, code, de.Code, de.Message)
}

func TestAuth_RefreshTokenFormat(t *testing.T) {
	h := newRefreshHarness(t)
	token, id := h.login(t)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	require.Len(t, parts[2], 43, "32 random bytes, URL-safe base64")
	row := h.repo.rows[id]
	require.Equal(t, hashRefreshSecret(parts[2]), row.TokenHash)
	require.NotContains(t, row.TokenHash, parts[2])
	require.Equal(t, h.clock.now, *row.SessionStartedAt)
	require.Equal(t, h.clock.now.Add(refreshTokenExpiration), row.ExpiresAt)
}

func TestAuth_LoginResult_CreateSessionError(t *testing.T) {
	h := newRefreshHarness(t)
	h.repo.createErr = errors.New("boom")
	_, err := h.auth.LoginResult(context.Background(), h.user)
	require.EqualError(t, err, "boom")
}

func TestAuth_LoginWithRefreshToken_RotatesInPlace(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)
	started := h.clock.now

	h.clock.now = started.Add(10 * 24 * time.Hour)
	got, err := h.auth.LoginWithRefreshToken(ctx, token)
	require.NoError(t, err)
	require.NotEmpty(t, got.AccessToken)
	require.NotEqual(t, token, got.RefreshToken)

	newID, newSecret, ok := h.auth.parseRefreshToken(got.RefreshToken)
	require.True(t, ok)
	require.Equal(t, id, newID, "the session keeps its id")
	row := h.repo.rows[id]
	require.Equal(t, hashRefreshSecret(newSecret), row.TokenHash)
	require.Equal(t, hashRefreshSecret(strings.Split(token, ".")[2]), row.PreviousHash)
	require.Equal(t, started, *row.SessionStartedAt)
	require.Equal(t, h.clock.now, *row.RotatedAt)
	require.Equal(t, h.clock.now.Add(refreshTokenExpiration), row.ExpiresAt)

	again, err := h.auth.LoginWithRefreshToken(ctx, got.RefreshToken)
	require.NoError(t, err)
	require.NotEqual(t, got.RefreshToken, again.RefreshToken)
	require.Empty(t, h.audit.entries)
}

func TestAuth_LoginWithRefreshToken_PreviousSecretWithinGrace(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)

	next, err := h.auth.LoginWithRefreshToken(ctx, token)
	require.NoError(t, err)

	h.clock.now = h.clock.now.Add(refreshGraceWindow)
	_, err = h.auth.LoginWithRefreshToken(ctx, token)
	require.True(t, domain.IsErrorNotFound(err), "a racing tab is answered not found: %v", err)
	require.True(t, h.repo.has(id))
	require.Empty(t, h.audit.entries)

	_, err = h.auth.LoginWithRefreshToken(ctx, next.RefreshToken)
	require.NoError(t, err)
}

func TestAuth_LoginWithRefreshToken_PreviousSecretAfterGraceRevokes(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)

	next, err := h.auth.LoginWithRefreshToken(ctx, token)
	require.NoError(t, err)

	h.clock.now = h.clock.now.Add(refreshGraceWindow + time.Second)
	_, err = h.auth.LoginWithRefreshToken(ctx, token)
	requireStatus(t, err, 401)
	require.False(t, h.repo.has(id))

	require.Len(t, h.audit.entries, 1)
	entry := h.audit.entries[0]
	require.Equal(t, domain.AuditAuthRefreshTokenReuseDetected, entry.Action)
	require.Equal(t, domain.AuditActorSystem, entry.ActorType)
	require.Equal(t, domain.AuditTargetUser, entry.TargetType)
	require.Equal(t, h.user.ID.Base36(), entry.TargetID)
	require.Equal(t, refreshReuseAfterGrace, entry.Metadata["reason"])
	require.Equal(t, []error{nil}, h.audit.txErrs, "the revocation commits")
	require.Equal(t, map[string]any{"session_id": "1", "reason": refreshReuseAfterGrace}, entry.Metadata)

	_, err = h.auth.LoginWithRefreshToken(ctx, next.RefreshToken)
	require.True(t, domain.IsErrorNotFound(err), "the legitimate holder is signed out too")
}

func TestAuth_LoginWithRefreshToken_OlderSecretRevokes(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	first, id := h.login(t)

	second, err := h.auth.LoginWithRefreshToken(ctx, first)
	require.NoError(t, err)
	_, err = h.auth.LoginWithRefreshToken(ctx, second.RefreshToken)
	require.NoError(t, err)

	_, err = h.auth.LoginWithRefreshToken(ctx, first)
	requireStatus(t, err, 401)
	require.False(t, h.repo.has(id))
	require.Len(t, h.audit.entries, 1)
	require.Equal(t, refreshReuseUnknownSecret, h.audit.entries[0].Metadata["reason"])
}

func TestAuth_LoginWithRefreshToken_ForgedTagIsUnknown(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)

	parts := strings.Split(token, ".")
	for _, forged := range []string{
		parts[0] + ".AAAAAAAAAAAAAAAAAAAAAA." + parts[2],
		parts[0] + "." + parts[1] + ".",
		"!!." + parts[1] + "." + parts[2],
	} {
		_, err := h.auth.LoginWithRefreshToken(ctx, forged)
		require.True(t, domain.IsErrorNotFound(err), "%q: %v", forged, err)
		require.NoError(t, h.auth.Logout(ctx, forged))
	}
	require.True(t, h.repo.has(id), "a session id alone cannot revoke a session")
	require.Empty(t, h.audit.entries)
}

func TestAuth_LoginWithRefreshToken_LostRaceIsNotFound(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)

	var winner string
	h.repo.beforeRot = func() {
		h.repo.beforeRot = nil
		got, err := h.auth.LoginWithRefreshToken(ctx, token)
		require.NoError(t, err)
		winner = got.RefreshToken
	}
	_, err := h.auth.LoginWithRefreshToken(ctx, token)
	require.True(t, domain.IsErrorNotFound(err), "%v", err)
	require.True(t, h.repo.has(id))
	require.Empty(t, h.audit.entries)

	_, err = h.auth.LoginWithRefreshToken(ctx, winner)
	require.NoError(t, err)
}

func TestAuth_LoginWithRefreshToken_ConcurrentRotationsHaveOneWinner(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.auth.LoginWithRefreshToken(ctx, token)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		require.True(t, domain.IsErrorNotFound(err), "%v", err)
	}
	require.Equal(t, 1, ok)
	require.True(t, h.repo.has(id))
}

func TestAuth_LoginWithRefreshToken_AbsoluteCap(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)
	start := h.clock.now

	for h.clock.now.Sub(start) < refreshSessionMaxAge-50*24*time.Hour {
		h.clock.now = h.clock.now.Add(50 * 24 * time.Hour)
		got, err := h.auth.LoginWithRefreshToken(ctx, token)
		require.NoError(t, err)
		token = got.RefreshToken
	}
	h.clock.now = start.Add(refreshSessionMaxAge + time.Second)
	_, err := h.auth.LoginWithRefreshToken(ctx, token)
	requireStatus(t, err, 400)
	require.EqualError(t, err, "refresh token expired")
	require.False(t, h.repo.has(id))
	require.Empty(t, h.audit.entries)
}

func TestAuth_LoginWithRefreshToken_Expired(t *testing.T) {
	h := newRefreshHarness(t)
	token, id := h.login(t)

	h.clock.now = h.clock.now.Add(refreshTokenExpiration + time.Second)
	_, err := h.auth.LoginWithRefreshToken(context.Background(), token)
	require.EqualError(t, err, "refresh token expired")
	require.False(t, h.repo.has(id))
}

func TestAuth_LoginWithRefreshToken_ExpiredDeleteError(t *testing.T) {
	h := newRefreshHarness(t)
	token, _ := h.login(t)
	h.repo.deleteErr = errors.New("delete failed")

	h.clock.now = h.clock.now.Add(refreshTokenExpiration + time.Second)
	_, err := h.auth.LoginWithRefreshToken(context.Background(), token)
	require.EqualError(t, err, "delete failed")
}

func TestAuth_LoginWithRefreshToken_UnverifiedEmail(t *testing.T) {
	h := newRefreshHarness(t)
	token, id := h.login(t)
	h.user.EmailVerified = false

	_, err := h.auth.LoginWithRefreshToken(context.Background(), token)
	requireStatus(t, err, 403)
	require.True(t, h.repo.has(id))
}

func TestAuth_LoginWithRefreshToken_UserLookupError(t *testing.T) {
	h := newRefreshHarness(t)
	token, _ := h.login(t)
	h.auth.userRepo = fakeRefreshUsers{err: domain.NewErrorRecordNotFound()}

	_, err := h.auth.LoginWithRefreshToken(context.Background(), token)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestAuth_LoginWithRefreshToken_RepositoryErrors(t *testing.T) {
	ctx := context.Background()

	h := newRefreshHarness(t)
	token, _ := h.login(t)
	h.repo.getErr = errors.New("get failed")
	_, err := h.auth.LoginWithRefreshToken(ctx, token)
	require.EqualError(t, err, "get failed")
	require.EqualError(t, h.auth.Logout(ctx, token), "get failed")

	h = newRefreshHarness(t)
	token, _ = h.login(t)
	h.repo.rotateErr = errors.New("rotate failed")
	_, err = h.auth.LoginWithRefreshToken(ctx, token)
	require.EqualError(t, err, "rotate failed")

	h = newRefreshHarness(t)
	token, id := h.login(t)
	h.repo.rows[id].TokenHash = "other"
	h.repo.deleteErr = errors.New("delete failed")
	_, err = h.auth.LoginWithRefreshToken(ctx, token)
	require.EqualError(t, err, "delete failed")
	require.Equal(t, []error{h.repo.deleteErr}, h.audit.txErrs)

	h = newRefreshHarness(t)
	token, id = h.login(t)
	h.repo.rows[id].TokenHash = "other"
	h.audit.recordErr = errors.New("audit failed")
	_, err = h.auth.LoginWithRefreshToken(ctx, token)
	require.EqualError(t, err, "audit failed")
}

func TestAuth_LoginWithRefreshToken_WithoutAudit(t *testing.T) {
	h := newRefreshHarness(t)
	h.auth.audit = nil
	token, id := h.login(t)
	h.repo.rows[id].TokenHash = "other"

	_, err := h.auth.LoginWithRefreshToken(context.Background(), token)
	requireStatus(t, err, 401)
	require.False(t, h.repo.has(id))
}

func TestAuth_LoginWithRefreshToken_UnknownSession(t *testing.T) {
	h := newRefreshHarness(t)
	ctx := context.Background()
	token, id := h.login(t)
	require.NoError(t, h.repo.DeleteRefreshSession(ctx, id))

	_, err := h.auth.LoginWithRefreshToken(ctx, token)
	require.True(t, domain.IsErrorNotFound(err))

	_, err = h.auth.LoginWithRefreshToken(ctx, "")
	require.True(t, domain.IsErrorNotFound(err))
	_, err = h.auth.LoginWithRefreshToken(ctx, "8a5c0f2e-1d2b-4c1e-9b7a-1234567890ab")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestAuth_Logout(t *testing.T) {
	ctx := context.Background()

	t.Run("current secret ends the session", func(t *testing.T) {
		h := newRefreshHarness(t)
		token, id := h.login(t)
		require.NoError(t, h.auth.Logout(ctx, token))
		require.False(t, h.repo.has(id))
	})

	t.Run("previous secret ends the session", func(t *testing.T) {
		h := newRefreshHarness(t)
		token, id := h.login(t)
		_, err := h.auth.LoginWithRefreshToken(ctx, token)
		require.NoError(t, err)
		require.NoError(t, h.auth.Logout(ctx, token))
		require.False(t, h.repo.has(id))
	})

	t.Run("wrong secret keeps the session", func(t *testing.T) {
		h := newRefreshHarness(t)
		token, id := h.login(t)
		parts := strings.Split(token, ".")
		require.NoError(t, h.auth.Logout(ctx, parts[0]+"."+parts[1]+".wrong-secret"))
		require.True(t, h.repo.has(id))
		require.Empty(t, h.audit.entries)
	})

	t.Run("unknown and empty tokens", func(t *testing.T) {
		h := newRefreshHarness(t)
		require.NoError(t, h.auth.Logout(ctx, ""))
		require.NoError(t, h.auth.Logout(ctx, "missing"))
		require.NoError(t, h.auth.Logout(ctx, h.auth.formatRefreshToken(42, "x")))
	})

	t.Run("delete error", func(t *testing.T) {
		h := newRefreshHarness(t)
		token, _ := h.login(t)
		h.repo.deleteErr = errors.New("delete failed")
		require.EqualError(t, h.auth.Logout(ctx, token), "delete failed")
	})
}

func TestParseRefreshToken(t *testing.T) {
	a := NewAuth("secret", nil, nil, nil)
	other := NewAuth("other-secret", nil, nil, nil)
	token := a.formatRefreshToken(35, "s3cret")
	require.True(t, strings.HasPrefix(token, "z."))

	id, secret, ok := a.parseRefreshToken(token)
	require.True(t, ok)
	require.Equal(t, int64(35), id)
	require.Equal(t, "s3cret", secret)

	_, _, ok = other.parseRefreshToken(token)
	require.False(t, ok, "the tag is bound to the server key")

	for _, bad := range []string{"", "8a5c0f2e-1d2b-4c1e-9b7a-1234567890ab", "a.b", "a.b.c.d", "0." + a.sessionTag("0") + ".x", "z." + a.sessionTag("z") + "."} {
		_, _, ok := a.parseRefreshToken(bad)
		require.False(t, ok, bad)
	}
}
