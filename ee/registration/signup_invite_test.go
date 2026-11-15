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
	"github.com/nipalab/nipa/internal/snow"
)

type stubInviteRepo struct {
	invites []*domain.SignupInvite
	err     error
}

func (r *stubInviteRepo) Upsert(_ context.Context, invite domain.SignupInvite) (*domain.SignupInvite, error) {
	if r.err != nil {
		return nil, r.err
	}
	invite.CreatedAt = time.Now()
	r.invites = append(r.invites, &invite)
	return &invite, nil
}

func (r *stubInviteRepo) List(context.Context) ([]*domain.SignupInvite, error) {
	return r.invites, r.err
}

func (r *stubInviteRepo) Delete(_ context.Context, id snow.ID) (bool, error) {
	for i, invite := range r.invites {
		if invite.ID == id {
			r.invites = append(r.invites[:i], r.invites[i+1:]...)
			return true, nil
		}
	}
	return false, r.err
}

func (r *stubInviteRepo) Allowed(_ context.Context, email string, now time.Time) (bool, error) {
	for _, invite := range r.invites {
		if invite.Email == email && (invite.ExpiresAt == nil || invite.ExpiresAt.After(now)) {
			return true, nil
		}
	}
	return false, r.err
}

type captureAudit struct{ entries []domain.AuditEntry }

func (c *captureAudit) Record(_ context.Context, entry domain.AuditEntry) error {
	c.entries = append(c.entries, entry)
	return nil
}

func newInvites(t *testing.T) (*SignupInvites, *stubInviteRepo, *captureAudit) {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	repo := &stubInviteRepo{}
	audit := &captureAudit{}
	return NewSignupInvites(repo, stubTransactor{}, node).WithAudit(audit), repo, audit
}

func superCtx() context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 9, IsSuperAdmin: true})
}

func TestSignupInvitesRequireSuperAdmin(t *testing.T) {
	invites, repo, _ := newInvites(t)
	plain := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 9})

	for _, ctx := range []context.Context{context.Background(), plain} {
		_, err := invites.Create(ctx, "bob@example.com", 0)
		require.Error(t, err)
		_, err = invites.List(ctx)
		require.Error(t, err)
		require.Error(t, invites.Revoke(ctx, 1))
	}
	require.Empty(t, repo.invites)
}

func TestSignupInvitesLifecycle(t *testing.T) {
	invites, repo, audit := newInvites(t)
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	invites.now = func() time.Time { return now }

	invite, err := invites.Create(superCtx(), " Bob@Example.com ", 7)
	require.NoError(t, err)
	require.Equal(t, "bob@example.com", invite.Email)
	require.Equal(t, snow.ID(9), invite.InvitedBy)
	require.WithinDuration(t, now.Add(7*24*time.Hour), *invite.ExpiresAt, time.Second)

	allowed, err := invites.Allowed(context.Background(), "bob@example.com")
	require.NoError(t, err)
	require.True(t, allowed)

	list, err := invites.List(superCtx())
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, invites.Revoke(superCtx(), invite.ID))
	require.Empty(t, repo.invites)
	require.Error(t, invites.Revoke(superCtx(), invite.ID), "a revoked invite is not found")

	require.Len(t, audit.entries, 2)
	require.Equal(t, domain.AuditSignupInviteCreated, audit.entries[0].Action)
	require.Equal(t, domain.AuditSignupInviteRevoked, audit.entries[1].Action)
}

func TestSignupInvitesValidation(t *testing.T) {
	invites, _, _ := newInvites(t)

	for _, email := range []string{"", "not-an-email", "Bob <bob@example.com>"} {
		_, err := invites.Create(superCtx(), email, 0)
		require.Error(t, err, email)
	}
	for _, days := range []int{-1, 366} {
		_, err := invites.Create(superCtx(), "bob@example.com", days)
		require.Error(t, err)
	}
}

func TestSignupInvitesRepositoryErrors(t *testing.T) {
	invites, repo, _ := newInvites(t)
	repo.err = errors.New("boom")

	_, err := invites.Create(superCtx(), "bob@example.com", 0)
	require.EqualError(t, err, "boom")
	_, err = invites.List(superCtx())
	require.EqualError(t, err, "boom")
	require.EqualError(t, invites.Revoke(superCtx(), 1), "boom")
	_, err = invites.Allowed(context.Background(), "bob@example.com")
	require.EqualError(t, err, "boom")
}

func TestSignupInviteRoutes(t *testing.T) {
	env := newRegistrationEnv(t)
	invites, _, _ := newInvites(t)
	handler := NewHandler(env.registration, nil).WithSignupInvites(invites)

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

	status, _ := do(http.MethodPost, "/admin/signup-invites", `{"email":"bob@example.com"}`, false)
	require.Equal(t, http.StatusForbidden, status)

	status, _ = do(http.MethodPost, "/admin/signup-invites", `not json`, true)
	require.Equal(t, http.StatusBadRequest, status)

	status, body := do(http.MethodPost, "/admin/signup-invites", `{"email":"bob@example.com","expires_in_days":3}`, true)
	require.Equal(t, http.StatusCreated, status)
	var created signupInviteResponse
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	require.Equal(t, "bob@example.com", created.Email)
	require.NotNil(t, created.ExpiresAt)

	status, body = do(http.MethodGet, "/admin/signup-invites", "", true)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "bob@example.com")

	status, _ = do(http.MethodDelete, "/admin/signup-invites/!!", "", true)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = do(http.MethodDelete, "/admin/signup-invites/"+created.ID, "", true)
	require.Equal(t, http.StatusNoContent, status)
	status, _ = do(http.MethodDelete, "/admin/signup-invites/"+created.ID, "", true)
	require.Equal(t, http.StatusNotFound, status)
}
