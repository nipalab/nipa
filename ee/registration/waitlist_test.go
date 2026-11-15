package registration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/snow"
)

type stubWaitlistRepo struct {
	entries   []*domain.WaitlistEntry
	err       error
	accounts  map[string]bool
	lastAfter snow.ID
	lastLimit int
}

func (r *stubWaitlistRepo) Join(_ context.Context, entry domain.WaitlistEntry) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	for _, existing := range r.entries {
		if existing.Email == entry.Email {
			return false, nil
		}
	}
	entry.CreatedAt = time.Now()
	r.entries = append(r.entries, &entry)
	return true, nil
}

func (r *stubWaitlistRepo) List(_ context.Context, after snow.ID, pending *bool, limit int) ([]*domain.WaitlistEntry, error) {
	r.lastAfter, r.lastLimit = after, limit
	if r.err != nil {
		return nil, r.err
	}
	var out []*domain.WaitlistEntry
	for _, entry := range r.entries {
		if entry.ID <= after || (pending != nil && (entry.NotifiedAt == nil) != *pending) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, entry)
	}
	return out, nil
}

func (r *stubWaitlistRepo) Delete(_ context.Context, id snow.ID) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	for i, entry := range r.entries {
		if entry.ID == id {
			r.entries = append(r.entries[:i], r.entries[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (r *stubWaitlistRepo) Pending(_ context.Context, limit int) ([]*domain.WaitlistEntry, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []*domain.WaitlistEntry
	for _, entry := range r.entries {
		if entry.NotifiedAt == nil && !r.accounts[entry.Email] && len(out) < limit {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (r *stubWaitlistRepo) MarkNotified(_ context.Context, ids []snow.ID, at time.Time) error {
	for _, id := range ids {
		for _, entry := range r.entries {
			if entry.ID == id {
				notified := at
				entry.NotifiedAt = &notified
			}
		}
	}
	return nil
}

type waitlistEnv struct {
	waitlist *Waitlist
	repo     *stubWaitlistRepo
	invites  *stubInviteRepo
	outbox   *stubOutbox
	kicker   *stubKicker
	audit    *captureAudit
}

func newWaitlistEnv(t *testing.T) *waitlistEnv {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	env := &waitlistEnv{
		repo:    &stubWaitlistRepo{accounts: map[string]bool{}},
		invites: &stubInviteRepo{},
		outbox:  &stubOutbox{},
		kicker:  &stubKicker{},
		audit:   &captureAudit{},
	}
	env.waitlist = NewWaitlist(env.repo, stubTransactor{}, node, "https://nipa.example.com/").
		WithMailer(env.outbox, env.kicker).WithAudit(env.audit).WithInvites(env.invites, true)
	env.waitlist.now = func() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC) }
	return env
}

func decodeDelivery(t *testing.T, delivery *domain.EmailDelivery) string {
	t.Helper()
	msg, err := mail.DecodeMessage(delivery.Body)
	require.NoError(t, err)
	return msg.MessageID + "\n" + msg.Text + "\n" + msg.HTML
}

func TestWaitlistJoinEmailsOnce(t *testing.T) {
	env := newWaitlistEnv(t)

	require.NoError(t, env.waitlist.Join(context.Background(), " Alice ", " Alice@Example.com ", WaitlistSourceRegistration))
	require.Len(t, env.repo.entries, 1)
	require.Equal(t, "alice@example.com", env.repo.entries[0].Email)
	require.Equal(t, "Alice", env.repo.entries[0].Name)
	require.Len(t, env.outbox.deliveries, 1)
	delivery := env.outbox.deliveries[0]
	require.Equal(t, waitlistJoinedEvent, delivery.Event)
	require.Equal(t, "alice@example.com", delivery.Email)
	require.Equal(t, snow.ID(0), delivery.UserID)
	require.Contains(t, decodeDelivery(t, delivery), "waitlist")
	require.Equal(t, 1, env.kicker.kicks)

	require.NoError(t, env.waitlist.Join(context.Background(), "Alice", "alice@example.com", WaitlistSourceOAuth))
	require.Len(t, env.repo.entries, 1)
	require.Len(t, env.outbox.deliveries, 1, "joining again sends nothing")
	require.Equal(t, 1, env.kicker.kicks)
}

func TestWaitlistJoinWithoutMailerAndErrors(t *testing.T) {
	env := newWaitlistEnv(t)
	env.waitlist.outbox = nil
	require.NoError(t, env.waitlist.Join(context.Background(), "", "bob@example.com", WaitlistSourceRegistration))
	require.Len(t, env.repo.entries, 1)

	env.repo.err = errors.New("db down")
	require.EqualError(t, env.waitlist.Join(context.Background(), "", "carol@example.com", WaitlistSourceRegistration), "db down")
}

func TestWaitlistAdminRequiresSuperAdmin(t *testing.T) {
	env := newWaitlistEnv(t)
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 3})

	_, err := env.waitlist.List(ctx, 0, nil, 0)
	require.True(t, domain.IsErrorNoPermission(err))
	_, err = env.waitlist.Notify(ctx, 0)
	require.True(t, domain.IsErrorNoPermission(err))
	require.True(t, domain.IsErrorNoPermission(env.waitlist.Remove(ctx, 1)))
}

func TestWaitlistNotifyAllowlistsAndEmails(t *testing.T) {
	env := newWaitlistEnv(t)
	ctx := context.Background()
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"} {
		require.NoError(t, env.waitlist.Join(ctx, "", email, WaitlistSourceRegistration))
	}
	env.repo.accounts["c@example.com"] = true
	env.outbox.deliveries = nil
	env.kicker.kicks = 0

	notified, err := env.waitlist.Notify(superCtx(), 2)
	require.NoError(t, err)
	require.Equal(t, 2, notified)
	require.Len(t, env.outbox.deliveries, 2)
	require.Equal(t, waitlistOpenEvent, env.outbox.deliveries[0].Event)
	require.Contains(t, decodeDelivery(t, env.outbox.deliveries[0]), "https://nipa.example.com/register")
	require.Len(t, env.invites.invites, 2)
	require.Equal(t, "a@example.com", env.invites.invites[0].Email)
	require.Equal(t, snow.ID(9), env.invites.invites[0].InvitedBy)
	require.Nil(t, env.invites.invites[0].ExpiresAt)
	require.Equal(t, 1, env.kicker.kicks)
	require.Len(t, env.audit.entries, 1)
	require.Equal(t, domain.AuditWaitlistNotified, env.audit.entries[0].Action)
	require.Equal(t, 2, env.audit.entries[0].Metadata["count"])

	notified, err = env.waitlist.Notify(superCtx(), 0)
	require.NoError(t, err)
	require.Equal(t, 1, notified, "c has an account, a and b were notified")
	require.Equal(t, "d@example.com", env.outbox.deliveries[2].Email)

	notified, err = env.waitlist.Notify(superCtx(), 0)
	require.NoError(t, err)
	require.Zero(t, notified)
	require.Equal(t, 2, env.kicker.kicks)
}

func TestWaitlistNotifyOpenRegistration(t *testing.T) {
	env := newWaitlistEnv(t)
	env.waitlist.WithInvites(env.invites, false)
	require.NoError(t, env.waitlist.Join(context.Background(), "", "a@example.com", WaitlistSourceRegistration))

	notified, err := env.waitlist.Notify(superCtx(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, notified)
	require.Empty(t, env.invites.invites, "open registration needs no allowlist")
	require.Equal(t, false, env.audit.entries[0].Metadata["allowlisted"])
}

func TestWaitlistNotifyWithoutMailer(t *testing.T) {
	env := newWaitlistEnv(t)
	require.NoError(t, env.waitlist.Join(context.Background(), "", "a@example.com", WaitlistSourceRegistration))
	env.waitlist.outbox = nil

	notified, err := env.waitlist.Notify(superCtx(), 0)
	require.NoError(t, err)
	require.Equal(t, 1, notified)
	require.NotNil(t, env.repo.entries[0].NotifiedAt)
	require.Equal(t, false, env.audit.entries[0].Metadata["emailed"])
}

func TestWaitlistNotifyValidationAndErrors(t *testing.T) {
	env := newWaitlistEnv(t)
	for _, limit := range []int{-1, maxWaitlistBatch + 1} {
		_, err := env.waitlist.Notify(superCtx(), limit)
		var domainErr *domain.Error
		require.ErrorAs(t, err, &domainErr)
		require.Equal(t, 400, domainErr.Code)
	}

	require.NoError(t, env.waitlist.Join(context.Background(), "", "a@example.com", WaitlistSourceRegistration))
	env.invites.err = errors.New("invite failed")
	_, err := env.waitlist.Notify(superCtx(), 0)
	require.EqualError(t, err, "invite failed")

	env.repo.err = errors.New("db down")
	_, err = env.waitlist.Notify(superCtx(), 0)
	require.EqualError(t, err, "db down")
}

func TestWaitlistListAndRemove(t *testing.T) {
	env := newWaitlistEnv(t)
	require.NoError(t, env.waitlist.Join(context.Background(), "", "a@example.com", WaitlistSourceRegistration))

	entries, err := env.waitlist.List(superCtx(), 0, nil, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, defaultWaitlistBatch, env.repo.lastLimit)

	id := entries[0].ID
	require.NoError(t, env.waitlist.Remove(superCtx(), id))
	require.Empty(t, env.repo.entries)
	require.Equal(t, domain.AuditWaitlistRemoved, env.audit.entries[0].Action)
	require.Equal(t, id.Base36(), env.audit.entries[0].TargetID)
	require.True(t, domain.IsErrorNotFound(env.waitlist.Remove(superCtx(), id)))

	env.repo.err = errors.New("db down")
	require.EqualError(t, env.waitlist.Remove(superCtx(), id), "db down")
}

func TestRegisterWaitlistsUninvitedAddress(t *testing.T) {
	env := newRegistrationEnv(t)
	wait := newWaitlistEnv(t)
	env.registration.cfg.InviteOnly = true
	env.registration.WithWaitlist(wait.waitlist)

	require.NoError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", false))
	require.Empty(t, env.repo.created)
	require.Empty(t, env.outbox.deliveries)
	require.Len(t, wait.repo.entries, 1)
	require.Equal(t, WaitlistSourceRegistration, wait.repo.entries[0].Source)
	require.Len(t, wait.outbox.deliveries, 1)

	env.repo.sweepErr = errors.New("sweep failed")
	require.EqualError(t, env.registration.Register(context.Background(), "Bob", "bob@example.com", false), "sweep failed")
	require.Len(t, wait.repo.entries, 1)
}

func TestWaitlistDeliveryHeaders(t *testing.T) {
	env := newWaitlistEnv(t)
	entry := &domain.WaitlistEntry{ID: 42, Email: "a@example.com", Name: "<Ann>"}
	delivery, err := env.waitlist.openDelivery(entry, time.Now())
	require.NoError(t, err)
	body := decodeDelivery(t, delivery)
	require.Contains(t, body, "nipa-waitlist-open-"+snow.ID(42).Base36())
	require.Contains(t, body, "&lt;Ann&gt;")
	require.Equal(t, "Hi,", greeting(""))
}

func TestWaitlistRoutes(t *testing.T) {
	env := newRegistrationEnv(t)
	wait := newWaitlistEnv(t)
	handler := NewHandler(env.registration, nil).WithWaitlist(wait.waitlist)

	ws := new(restful.WebService)
	ws.Path("/api/v1").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	ws.Filter(func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		if req.HeaderParameter("X-Super") == "1" {
			req.Request = req.Request.WithContext(superCtx())
		}
		chain.ProcessFilter(req, resp)
	})
	RegisterRoutes(ws, handler)
	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)

	do := func(method, path, body string, super bool) (int, string) {
		req, err := http.NewRequest(method, server.URL+"/api/v1"+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if super {
			req.Header.Set("X-Super", "1")
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(data)
	}

	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		require.NoError(t, wait.waitlist.Join(context.Background(), "", email, WaitlistSourceRegistration))
	}

	status, _ := do(http.MethodGet, "/admin/waitlist", "", false)
	require.Equal(t, http.StatusForbidden, status)
	for _, query := range []string{"?pending=maybe", "?after=!!", "?limit=x", "?limit=0", "?limit=1001"} {
		status, _ = do(http.MethodGet, "/admin/waitlist"+query, "", true)
		require.Equal(t, http.StatusBadRequest, status, query)
	}

	status, body := do(http.MethodGet, "/admin/waitlist?limit=2&pending=true", "", true)
	require.Equal(t, http.StatusOK, status)
	var page waitlistListResponse
	require.NoError(t, json.Unmarshal([]byte(body), &page))
	require.Len(t, page.Entries, 2)
	require.Equal(t, 3, wait.repo.lastLimit, "over-fetches one row")
	require.Equal(t, page.Entries[1].ID, page.NextCursor)

	status, body = do(http.MethodGet, "/admin/waitlist?limit=2&after="+page.NextCursor, "", true)
	require.Equal(t, http.StatusOK, status)
	page = waitlistListResponse{}
	require.NoError(t, json.Unmarshal([]byte(body), &page))
	require.Len(t, page.Entries, 1)
	require.Empty(t, page.NextCursor)
	require.Equal(t, "c@example.com", page.Entries[0].Email)

	status, _ = do(http.MethodPost, "/admin/waitlist/notify", "not json", true)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = do(http.MethodPost, "/admin/waitlist/notify", `{"limit":5000}`, true)
	require.Equal(t, http.StatusBadRequest, status)
	status, body = do(http.MethodPost, "/admin/waitlist/notify", `{"limit":2}`, true)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"notified":2}`, body)

	status, body = do(http.MethodGet, "/admin/waitlist?pending=false", "", true)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "notified_at")

	status, _ = do(http.MethodDelete, "/admin/waitlist/!!", "", true)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = do(http.MethodDelete, "/admin/waitlist/"+page.Entries[0].ID, "", true)
	require.Equal(t, http.StatusNoContent, status)
	status, _ = do(http.MethodDelete, "/admin/waitlist/"+page.Entries[0].ID, "", true)
	require.Equal(t, http.StatusNotFound, status)

	wait.repo.err = errors.New("db down")
	status, _ = do(http.MethodGet, "/admin/waitlist", "", true)
	require.Equal(t, http.StatusInternalServerError, status)
	status, _ = do(http.MethodPost, "/admin/waitlist/notify", `{}`, true)
	require.Equal(t, http.StatusInternalServerError, status)
}
