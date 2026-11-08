package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	imail "github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
)

type invHarness struct {
	uc      *OrgInvitation
	invs    *MockorgInvitationRepository
	members *MockorgMemberRepository
	users   *MockuserLookup
	outbox  *MockinvitationOutbox
	kicker  *MockdispatcherKicker
	rec     *captureRecorder
	cache   *fakePermissionCache
	now     time.Time
}

func newInvHarness(t *testing.T) *invHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	h := &invHarness{
		invs:    NewMockorgInvitationRepository(ctrl),
		members: NewMockorgMemberRepository(ctrl),
		users:   NewMockuserLookup(ctrl),
		outbox:  NewMockinvitationOutbox(ctrl),
		kicker:  NewMockdispatcherKicker(ctrl),
		rec:     &captureRecorder{},
		cache:   &fakePermissionCache{},
		now:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	org := NewOrg(h.members, node).WithAudit(h.rec).WithPermissionCache(h.cache)
	h.uc = NewOrgInvitation(h.invs, org, h.users, node)
	h.uc.now = func() time.Time { return h.now }
	return h
}

func (h *invHarness) withMail() *invHarness {
	h.uc.WithOutbox(h.outbox, "https://nipa.example/").WithKicker(h.kicker)
	return h
}

var (
	testOrgRef   = &domain.Organization{ID: 1, Name: "Acme <Studio>", Slug: "acme"}
	ownerCtx     = func() context.Context { return permissionCtx(42, withAdmin()) }
	inviteeCtx   = func() context.Context { return permissionCtx(7) }
	verifiedBob  = &domain.User{ID: 7, Name: "Bob", Email: "Bob@Example.com", EmailVerified: true}
	pendingInvOf = func(h *invHarness, token string) *domain.OrgInvitation {
		return &domain.OrgInvitation{
			ID: 99, OrgID: 1, OrgName: "Acme", OrgSlug: "acme", InvitedBy: 42, InviterName: "Owner",
			Email: "bob@example.com", Role: domain.OrgRoleMember, Status: domain.OrgInvitationPending,
			TokenHash: hashInvitationToken(token), ExpiresAt: h.now.Add(time.Hour),
		}
	}
)

func (h *invHarness) expectCreateBase(members ...*domain.OrgMember) {
	h.members.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(members, nil)
	h.invs.EXPECT().ExpireStale(gomock.Any(), snow.ID(1), "bob@example.com", h.now).Return(nil)
}

func TestOrgInvitation_Create(t *testing.T) {
	t.Run("new invitation is stored, mailed and audited", func(t *testing.T) {
		h := newInvHarness(t).withMail()
		h.expectCreateBase(&domain.OrgMember{User: domain.User{Email: "owner@example.com"}})
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(nil, domain.NewErrorNotFound("x"))
		var stored *domain.OrgInvitation
		h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, inv *domain.OrgInvitation) error {
			stored = inv
			return nil
		})
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{Name: "Owner <o>"}, nil)
		var delivery *domain.EmailDelivery
		h.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d []*domain.EmailDelivery) error {
			require.Len(t, d, 1)
			delivery = d[0]
			return nil
		})
		h.kicker.EXPECT().Kick()

		require.NoError(t, h.uc.Create(ownerCtx(), testOrgRef, "  Bob@Example.com ", ""))

		require.Equal(t, "bob@example.com", stored.Email)
		require.Equal(t, domain.OrgRoleMember, stored.Role)
		require.Equal(t, snow.ID(42), stored.InvitedBy)
		require.Len(t, stored.TokenHash, 32)
		require.Equal(t, h.now.Add(orgInvitationTTL), stored.ExpiresAt)

		require.Equal(t, orgInvitationEvent, delivery.Event)
		require.Equal(t, "bob@example.com", delivery.Email)
		msg, err := imail.DecodeMessage(delivery.Body)
		require.NoError(t, err)
		require.Equal(t, []string{"bob@example.com"}, msg.To)
		require.Contains(t, msg.Text, "https://nipa.example/invitations/")
		token := msg.Text[strings.Index(msg.Text, "/invitations/")+len("/invitations/"):]
		token = token[:strings.IndexAny(token, "\n ")]
		require.Equal(t, stored.TokenHash, hashInvitationToken(token), "the mailed token hashes to the stored value")
		require.NotContains(t, msg.HTML, "<Studio>")
		require.Contains(t, msg.HTML, "&lt;Studio&gt;")

		entry := h.rec.only(t)
		requireActor(t, entry, domain.AuditOrgInvitationCreated)
		require.Equal(t, "example.com", entry.Metadata["email_domain"])
		require.NotContains(t, entry.Metadata, "email")
	})

	t.Run("known and unknown addresses get the identical outcome", func(t *testing.T) {
		for _, email := range []string{"bob@example.com", "stranger@example.com"} {
			h := newInvHarness(t)
			h.members.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(nil, nil)
			h.invs.EXPECT().ExpireStale(gomock.Any(), snow.ID(1), email, h.now).Return(nil)
			h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), email).Return(nil, domain.NewErrorNotFound("x"))
			h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			require.NoError(t, h.uc.Create(ownerCtx(), testOrgRef, email, domain.OrgRoleOwner))
		}
	})

	t.Run("non owner is refused before anything else", func(t *testing.T) {
		h := newInvHarness(t)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(9)).Return(domain.OrgRoleMember, nil)
		err := h.uc.Create(permissionCtx(9), testOrgRef, "bob@example.com", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("anonymous caller is refused", func(t *testing.T) {
		h := newInvHarness(t)
		err := h.uc.Create(context.Background(), testOrgRef, "bob@example.com", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("invalid input", func(t *testing.T) {
		h := newInvHarness(t)
		requireUserError(t, h.uc.Create(ownerCtx(), testOrgRef, "not-an-email", ""))
		requireUserError(t, h.uc.Create(ownerCtx(), testOrgRef, "Bob <bob@example.com>", ""))
		requireUserError(t, h.uc.Create(ownerCtx(), testOrgRef, "<bob@example.com>", ""))
		requireUserError(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", "viewer"))
	})

	t.Run("already a member", func(t *testing.T) {
		h := newInvHarness(t)
		h.members.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return([]*domain.OrgMember{{User: domain.User{Email: "BOB@example.com"}}}, nil)
		err := h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", "")
		require.True(t, domain.IsErrorConflict(err))
	})

	t.Run("pending invitation with the same role is rotated", func(t *testing.T) {
		h := newInvHarness(t)
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(&domain.OrgInvitation{ID: 5, Role: domain.OrgRoleMember}, nil)
		h.invs.EXPECT().Rotate(gomock.Any(), snow.ID(5), gomock.Any(), h.now.Add(orgInvitationTTL)).Return(true, nil)
		require.NoError(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", "member"))
		requireActor(t, h.rec.only(t), domain.AuditOrgInvitationResent)
	})

	t.Run("pending invitation with another role is replaced", func(t *testing.T) {
		h := newInvHarness(t)
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(&domain.OrgInvitation{ID: 5, Role: domain.OrgRoleMember}, nil)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(5), domain.OrgInvitationRevoked).Return(true, nil)
		h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		require.NoError(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", "owner"))
		requireActor(t, h.rec.only(t), domain.AuditOrgInvitationCreated)
	})

	t.Run("rotate lost the race", func(t *testing.T) {
		h := newInvHarness(t)
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(&domain.OrgInvitation{ID: 5, Role: domain.OrgRoleMember}, nil)
		h.invs.EXPECT().Rotate(gomock.Any(), snow.ID(5), gomock.Any(), gomock.Any()).Return(false, nil)
		require.True(t, domain.IsErrorConflict(h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", "")))
	})

	t.Run("repository failures surface", func(t *testing.T) {
		boom := errors.New("boom")
		h := newInvHarness(t)
		h.members.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(nil, boom)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), boom)

		h = newInvHarness(t)
		h.members.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(nil, nil)
		h.invs.EXPECT().ExpireStale(gomock.Any(), snow.ID(1), "bob@example.com", h.now).Return(boom)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), boom)

		h = newInvHarness(t)
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(nil, boom)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), boom)

		h = newInvHarness(t)
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(&domain.OrgInvitation{ID: 5, Role: domain.OrgRoleOwner}, nil)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(5), domain.OrgInvitationRevoked).Return(false, boom)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), boom)

		h = newInvHarness(t)
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(nil, domain.NewErrorNotFound("x"))
		h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(boom)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), boom)

		h = newInvHarness(t).withMail()
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(nil, domain.NewErrorNotFound("x"))
		h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(nil, boom)
		h.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(boom)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), boom, "a mail failure rolls the invitation back")
		require.Empty(t, h.rec.entries)
	})

	t.Run("audit failure fails the invitation", func(t *testing.T) {
		h := newInvHarness(t)
		h.rec.err = errAudit
		h.expectCreateBase()
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(nil, domain.NewErrorNotFound("x"))
		h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		require.ErrorIs(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""), errAudit)
	})
}

func TestOrgInvitation_CreateRateLimits(t *testing.T) {
	limiter := func(scope string) *ratelimit.Limiter {
		return ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{scope: {Rate: 0.0001, Burst: 1}})
	}
	for _, scope := range []string{ratelimit.ScopeInviteUser, ratelimit.ScopeInviteEmail} {
		h := newInvHarness(t)
		h.uc.WithLimiter(limiter(scope))
		h.members.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(nil, nil)
		h.invs.EXPECT().ExpireStale(gomock.Any(), snow.ID(1), "bob@example.com", h.now).Return(nil)
		h.invs.EXPECT().FindPending(gomock.Any(), snow.ID(1), "bob@example.com").Return(nil, domain.NewErrorNotFound("x"))
		h.invs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		require.NoError(t, h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", ""))

		err := h.uc.Create(ownerCtx(), testOrgRef, "bob@example.com", "")
		var derr *domain.Error
		require.ErrorAs(t, err, &derr, scope)
		require.Equal(t, 429, derr.Code, scope)
	}
}

func TestOrgInvitation_List(t *testing.T) {
	h := newInvHarness(t)
	rows := func() []*domain.OrgInvitation {
		return []*domain.OrgInvitation{
			{ID: 1, Status: domain.OrgInvitationPending, ExpiresAt: h.now.Add(time.Hour)},
			{ID: 2, Status: domain.OrgInvitationPending, ExpiresAt: h.now.Add(-time.Hour)},
		}
	}

	h.invs.EXPECT().ListByOrg(gomock.Any(), snow.ID(1), "", h.now).Return(rows(), nil)
	got, err := h.uc.List(ownerCtx(), 1, "")
	require.NoError(t, err)
	require.Equal(t, domain.OrgInvitationPending, got[0].Status)
	require.Equal(t, domain.OrgInvitationExpired, got[1].Status, "lazily expired rows read as expired")

	h.invs.EXPECT().ListByOrg(gomock.Any(), snow.ID(1), domain.OrgInvitationPending, h.now).Return(rows()[:1], nil)
	got, err = h.uc.List(ownerCtx(), 1, domain.OrgInvitationPending)
	require.NoError(t, err)
	require.Len(t, got, 1)

	_, err = h.uc.List(ownerCtx(), 1, "bogus")
	requireUserError(t, err)

	h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(9)).Return(domain.OrgRoleMember, nil)
	_, err = h.uc.List(permissionCtx(9), 1, "")
	require.True(t, domain.IsErrorNoPermission(err))

	h.invs.EXPECT().ListByOrg(gomock.Any(), snow.ID(1), "", h.now).Return(nil, errors.New("boom"))
	_, err = h.uc.List(ownerCtx(), 1, "")
	require.Error(t, err)
}

func TestOrgInvitation_Revoke(t *testing.T) {
	t.Run("revokes a pending invitation", func(t *testing.T) {
		h := newInvHarness(t)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 1, Email: "a@b.co", Role: "member"}, nil)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(5), domain.OrgInvitationRevoked).Return(true, nil)
		require.NoError(t, h.uc.Revoke(ownerCtx(), 1, 5))
		requireActor(t, h.rec.only(t), domain.AuditOrgInvitationRevoked)
	})

	t.Run("not pending anymore", func(t *testing.T) {
		h := newInvHarness(t)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 1}, nil)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(5), domain.OrgInvitationRevoked).Return(false, nil)
		require.True(t, domain.IsErrorConflict(h.uc.Revoke(ownerCtx(), 1, 5)))
		require.Empty(t, h.rec.entries)
	})

	t.Run("other org's invitation is not found", func(t *testing.T) {
		h := newInvHarness(t)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 2}, nil)
		require.True(t, domain.IsErrorNotFound(h.uc.Revoke(ownerCtx(), 1, 5)))
	})

	t.Run("non owner and lookup and resolve errors", func(t *testing.T) {
		h := newInvHarness(t)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(9)).Return(domain.OrgRoleMember, nil)
		require.True(t, domain.IsErrorNoPermission(h.uc.Revoke(permissionCtx(9), 1, 5)))

		boom := errors.New("boom")
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(nil, boom)
		require.ErrorIs(t, h.uc.Revoke(ownerCtx(), 1, 5), boom)

		h.invs.EXPECT().Get(gomock.Any(), snow.ID(6)).Return(&domain.OrgInvitation{ID: 6, OrgID: 1}, nil)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(6), domain.OrgInvitationRevoked).Return(false, boom)
		require.ErrorIs(t, h.uc.Revoke(ownerCtx(), 1, 6), boom)
	})
}

func TestOrgInvitation_Resend(t *testing.T) {
	t.Run("rotates and mails again", func(t *testing.T) {
		h := newInvHarness(t).withMail()
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 1, OrgName: "Acme", Email: "bob@example.com", Role: "member"}, nil)
		h.invs.EXPECT().Rotate(gomock.Any(), snow.ID(5), gomock.Any(), h.now.Add(orgInvitationTTL)).Return(true, nil)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{Name: "Owner"}, nil)
		h.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)
		h.kicker.EXPECT().Kick()
		require.NoError(t, h.uc.Resend(ownerCtx(), 1, 5))
		requireActor(t, h.rec.only(t), domain.AuditOrgInvitationResent)
	})

	t.Run("not pending anymore", func(t *testing.T) {
		h := newInvHarness(t)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 1, Email: "bob@example.com"}, nil)
		h.invs.EXPECT().Rotate(gomock.Any(), snow.ID(5), gomock.Any(), gomock.Any()).Return(false, nil)
		require.True(t, domain.IsErrorConflict(h.uc.Resend(ownerCtx(), 1, 5)))
	})

	t.Run("rotate error and throttle and non owner", func(t *testing.T) {
		boom := errors.New("boom")
		h := newInvHarness(t)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 1, Email: "bob@example.com"}, nil)
		h.invs.EXPECT().Rotate(gomock.Any(), snow.ID(5), gomock.Any(), gomock.Any()).Return(false, boom)
		require.ErrorIs(t, h.uc.Resend(ownerCtx(), 1, 5), boom)

		h = newInvHarness(t)
		h.uc.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
			ratelimit.ScopeInviteUser: {Rate: 0.0001, Burst: 1},
		}))
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(5)).Return(&domain.OrgInvitation{ID: 5, OrgID: 1, Email: "bob@example.com"}, nil).Times(2)
		h.invs.EXPECT().Rotate(gomock.Any(), snow.ID(5), gomock.Any(), gomock.Any()).Return(true, nil)
		require.NoError(t, h.uc.Resend(ownerCtx(), 1, 5))
		var derr *domain.Error
		require.ErrorAs(t, h.uc.Resend(ownerCtx(), 1, 5), &derr)
		require.Equal(t, 429, derr.Code)

		h = newInvHarness(t)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(9)).Return(domain.OrgRoleMember, nil)
		require.True(t, domain.IsErrorNoPermission(h.uc.Resend(permissionCtx(9), 1, 5)))
	})
}

func TestOrgInvitation_ListMine(t *testing.T) {
	h := newInvHarness(t)
	want := []*domain.OrgInvitation{{ID: 1}}
	h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
	h.invs.EXPECT().ListPendingByEmail(gomock.Any(), "bob@example.com", h.now).Return(want, nil)
	got, err := h.uc.ListMine(inviteeCtx())
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = h.uc.ListMine(context.Background())
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 401, derr.Code)

	h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Email: "bob@example.com"}, nil)
	_, err = h.uc.ListMine(inviteeCtx())
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 403, derr.Code, "unverified addresses see nothing")

	h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Deleted: true, EmailVerified: true}, nil)
	_, err = h.uc.ListMine(inviteeCtx())
	require.True(t, domain.IsErrorNoPermission(err))

	h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(nil, errors.New("boom"))
	_, err = h.uc.ListMine(inviteeCtx())
	require.Error(t, err)
}

func TestOrgInvitation_AcceptByToken(t *testing.T) {
	const token = "tok-123"

	t.Run("accepts, applies the role, invalidates the cache and audits", func(t *testing.T) {
		h := newInvHarness(t)
		inv := pendingInvOf(h, token)
		inv.Role = domain.OrgRoleOwner
		gate := NewMockSeatGate(gomock.NewController(t))
		h.uc.orgs.WithSeatGate(gate)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(inv, nil)
		gate.EXPECT().Admit(gomock.Any(), snow.ID(1), snow.ID(7), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _, _ snow.ID, insert func(context.Context) error) error { return insert(ctx) })
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", domain.NewErrorNotFound("x"))
		h.members.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(nil)
		h.invs.EXPECT().Accept(gomock.Any(), snow.ID(99), snow.ID(7), h.now).Return(true, nil)

		got, err := h.uc.Accept(inviteeCtx(), token)
		require.NoError(t, err)
		require.Equal(t, domain.OrgInvitationAccepted, got.Status)
		require.Equal(t, 1, h.cache.invalidated)
		entry := h.rec.only(t)
		requireActor(t, entry, domain.AuditOrgInvitationAccepted)
		require.Equal(t, snow.ID(7).Base36(), entry.Metadata["user_id"])
		require.Equal(t, snow.ID(99).Base36(), entry.TargetID)
	})

	t.Run("seat limit keeps the invitation pending", func(t *testing.T) {
		h := newInvHarness(t)
		gate := NewMockSeatGate(gomock.NewController(t))
		h.uc.orgs.WithSeatGate(gate)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(pendingInvOf(h, token), nil)
		gate.EXPECT().Admit(gomock.Any(), snow.ID(1), snow.ID(7), gomock.Any()).Return(domain.NewErrorQuotaExceeded("seat limit reached"))

		_, err := h.uc.Accept(inviteeCtx(), token)
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, 402, derr.Code)
		require.Empty(t, h.rec.entries)
		require.Zero(t, h.cache.invalidated)
	})

	t.Run("already a member keeps the role and consumes the invitation", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(pendingInvOf(h, token), nil)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleOwner, nil)
		h.invs.EXPECT().Accept(gomock.Any(), snow.ID(99), snow.ID(7), h.now).Return(true, nil)
		_, err := h.uc.Accept(inviteeCtx(), token)
		require.NoError(t, err)
	})

	t.Run("lost the race", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(pendingInvOf(h, token), nil)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
		h.invs.EXPECT().Accept(gomock.Any(), snow.ID(99), snow.ID(7), h.now).Return(false, nil)
		_, err := h.uc.Accept(inviteeCtx(), token)
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("write errors surface", func(t *testing.T) {
		boom := errors.New("boom")
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil).Times(3)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).Return(pendingInvOf(h, token), nil).Times(3)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", boom)
		_, err := h.uc.Accept(inviteeCtx(), token)
		require.ErrorIs(t, err, boom)

		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", domain.NewErrorNotFound("x"))
		h.members.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleMember).Return(boom)
		_, err = h.uc.Accept(inviteeCtx(), token)
		require.ErrorIs(t, err, boom)

		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
		h.invs.EXPECT().Accept(gomock.Any(), snow.ID(99), snow.ID(7), h.now).Return(false, boom)
		_, err = h.uc.Accept(inviteeCtx(), token)
		require.ErrorIs(t, err, boom)
	})
}

func TestOrgInvitation_InviteeRejections(t *testing.T) {
	const token = "tok-123"
	notFound := func(t *testing.T, err error) {
		t.Helper()
		require.True(t, domain.IsErrorNotFound(err), "got %v", err)
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, "invitation not found", derr.Message, "every rejection reads the same")
	}

	t.Run("wrong token", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken("bad")).Return(nil, domain.NewErrorNotFound("record not found"))
		_, err := h.uc.Accept(inviteeCtx(), "bad")
		notFound(t, err)
	})

	t.Run("empty and oversized tokens never hit the repository", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil).Times(2)
		_, err := h.uc.Accept(inviteeCtx(), "")
		notFound(t, err)
		_, err = h.uc.Accept(inviteeCtx(), strings.Repeat("a", maxInvitationToken+1))
		notFound(t, err)
	})

	t.Run("stored hash does not match", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(pendingInvOf(h, "other"), nil)
		_, err := h.uc.Accept(inviteeCtx(), token)
		notFound(t, err)
	})

	t.Run("email mismatch", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Email: "mallory@example.com", EmailVerified: true}, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(pendingInvOf(h, token), nil)
		_, err := h.uc.Accept(inviteeCtx(), token)
		notFound(t, err)
	})

	for _, status := range []string{domain.OrgInvitationRevoked, domain.OrgInvitationDeclined, domain.OrgInvitationAccepted, domain.OrgInvitationExpired} {
		t.Run("status "+status, func(t *testing.T) {
			h := newInvHarness(t)
			inv := pendingInvOf(h, token)
			inv.Status = status
			h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
			h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(inv, nil)
			_, err := h.uc.Preview(inviteeCtx(), token)
			notFound(t, err)
		})
	}

	t.Run("expired pending invitation", func(t *testing.T) {
		h := newInvHarness(t)
		inv := pendingInvOf(h, token)
		inv.ExpiresAt = h.now.Add(-time.Second)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(inv, nil)
		_, err := h.uc.Accept(inviteeCtx(), token)
		require.True(t, domain.IsErrorConflict(err))
		require.Empty(t, h.rec.entries)
	})

	t.Run("unverified email", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Email: "bob@example.com"}, nil)
		_, err := h.uc.Accept(inviteeCtx(), token)
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, 403, derr.Code)
	})

	t.Run("repository error is not masked", func(t *testing.T) {
		h := newInvHarness(t)
		boom := errors.New("boom")
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).Return(nil, boom)
		_, err := h.uc.Preview(inviteeCtx(), token)
		require.ErrorIs(t, err, boom)
	})

	t.Run("token lookups are throttled per user", func(t *testing.T) {
		h := newInvHarness(t)
		h.uc.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
			ratelimit.ScopeInviteToken: {Rate: 0.0001, Burst: 1},
		}))
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil).Times(2)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorNotFound("x"))
		_, err := h.uc.Preview(inviteeCtx(), "guess-1")
		notFound(t, err)
		_, err = h.uc.Preview(inviteeCtx(), "guess-2")
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, 429, derr.Code)
	})
}

func TestOrgInvitation_ByIDAndDecline(t *testing.T) {
	const token = "tok-123"

	t.Run("accept by id", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(99)).Return(pendingInvOf(h, token), nil)
		h.members.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", domain.NewErrorNotFound("x"))
		h.members.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleMember).Return(nil)
		h.invs.EXPECT().Accept(gomock.Any(), snow.ID(99), snow.ID(7), h.now).Return(true, nil)
		_, err := h.uc.AcceptByID(inviteeCtx(), 99)
		require.NoError(t, err)
	})

	t.Run("someone else's id looks missing", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(8)).Return(&domain.User{ID: 8, Email: "eve@example.com", EmailVerified: true}, nil)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(99)).Return(pendingInvOf(h, token), nil)
		_, err := h.uc.AcceptByID(permissionCtx(8), 99)
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("decline by token and by id", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil).Times(2)
		h.invs.EXPECT().GetByTokenHash(gomock.Any(), hashInvitationToken(token)).Return(pendingInvOf(h, token), nil)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(99)).Return(pendingInvOf(h, token), nil)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(99), domain.OrgInvitationDeclined).Return(true, nil).Times(2)
		require.NoError(t, h.uc.Decline(inviteeCtx(), token))
		require.NoError(t, h.uc.DeclineByID(inviteeCtx(), 99))
		require.Len(t, h.rec.entries, 2)
		requireActor(t, h.rec.entries[0], domain.AuditOrgInvitationDeclined)
		require.Zero(t, h.cache.invalidated)
	})

	t.Run("decline errors", func(t *testing.T) {
		h := newInvHarness(t)
		h.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(verifiedBob, nil).Times(3)
		h.invs.EXPECT().Get(gomock.Any(), snow.ID(99)).Return(pendingInvOf(h, token), nil).Times(2)
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(99), domain.OrgInvitationDeclined).Return(false, nil)
		require.True(t, domain.IsErrorNotFound(h.uc.DeclineByID(inviteeCtx(), 99)))

		boom := errors.New("boom")
		h.invs.EXPECT().Resolve(gomock.Any(), snow.ID(99), domain.OrgInvitationDeclined).Return(false, boom)
		require.ErrorIs(t, h.uc.DeclineByID(inviteeCtx(), 99), boom)

		h.invs.EXPECT().Get(gomock.Any(), snow.ID(1)).Return(nil, domain.NewErrorNotFound("x"))
		require.True(t, domain.IsErrorNotFound(h.uc.DeclineByID(inviteeCtx(), 1)))
	})
}

func TestInvitationToken(t *testing.T) {
	token, hash, err := newInvitationToken()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(token), 43)
	require.Equal(t, hashInvitationToken(token), hash)
	other, _, err := newInvitationToken()
	require.NoError(t, err)
	require.NotEqual(t, token, other)
}
