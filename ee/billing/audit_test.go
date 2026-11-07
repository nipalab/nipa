package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var errAudit = errors.New("audit down")

type captureRecorder struct {
	entries []domain.AuditEntry
	inTx    []bool
	err     error
}

func (c *captureRecorder) Record(ctx context.Context, entry domain.AuditEntry) error {
	if c.err != nil {
		return c.err
	}
	c.entries = append(c.entries, entry)
	c.inTx = append(c.inTx, inTx(ctx))
	return nil
}

func (c *captureRecorder) actions() []string {
	out := make([]string, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e.Action)
	}
	return out
}

func claimCtx(userID snow.ID) context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsSuperAdmin: true})
}

func audited() (*Service, *stubRepo, *stubLedger, *stubProvider, *captureRecorder) {
	svc, repo, ledger, provider := newProviderService()
	rec := &captureRecorder{}
	svc.WithAudit(rec)
	return svc, repo, ledger, provider, rec
}

func TestAudit_ApplyFromWebhookUsesTheProviderActor(t *testing.T) {
	svc, repo, ledger, _ := eventService(payer, "ctm_1")
	rec := &captureRecorder{}
	svc.WithAudit(rec)
	old := int64(1000)
	ledger.computed = &old
	repo.live = &Subscription{ProviderSubscriptionID: "sub_1", Status: StatusTrialing, Seats: 2, Interval: IntervalMonth}

	require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")))
	require.Len(t, rec.entries, 1)
	entry := rec.entries[0]
	require.True(t, rec.inTx[0], "recorded inside the apply transaction")
	require.Equal(t, domain.AuditBillingSubscriptionApplied, entry.Action)
	require.Equal(t, domain.AuditActorProvider, entry.ActorType)
	require.Equal(t, "stub", entry.ActorLabel)
	require.Equal(t, domain.AuditTargetSubscription, entry.TargetType)
	require.Equal(t, "sub_1", entry.TargetID)
	require.Equal(t, "evt_1", entry.Metadata["event_id"])
	require.Equal(t, "subscription.activated", entry.Metadata["event_type"])
	require.Equal(t, payer.Base36(), entry.Metadata["owner_id"])
	oldMeta := entry.Metadata["old"].(map[string]any)
	require.Equal(t, StatusTrialing, oldMeta["status"])
	require.Equal(t, int64(2), oldMeta["seats"])
	require.Equal(t, int64(1000), oldMeta["quota_bytes"])
	newMeta := entry.Metadata["new"].(map[string]any)
	require.Equal(t, StatusActive, newMeta["status"])
	require.Equal(t, int64(5), newMeta["seats"])
	require.Equal(t, testConfig.Entitlement(&Subscription{Status: StatusActive, Seats: 5}).QuotaBytes, newMeta["quota_bytes"])

	require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")))
	require.Len(t, rec.entries, 1, "a duplicate delivery records nothing")
}

func TestAudit_ApplySkipsAStaleUpdate(t *testing.T) {
	svc, repo, _, _ := eventService(payer, "")
	rec := &captureRecorder{}
	svc.WithAudit(rec)
	repo.stale = true

	require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_old")))
	require.Empty(t, rec.entries, "an older event writes nothing and records nothing")
	require.NotNil(t, repo.events["evt_old"].ProcessedAt)
}

func TestAudit_ApplyWithoutPriorState(t *testing.T) {
	svc, _, _, _, rec := audited()
	require.NoError(t, svc.Apply(claimCtx(9), Subscription{ProviderSubscriptionID: "sub_x", UserID: payer, Status: StatusActive, Seats: 1, ProviderUpdatedAt: at(1)}))
	entry := rec.entries[0]
	require.Empty(t, entry.ActorType, "the claim wins over the provider")
	require.Equal(t, map[string]any{"quota_bytes": nil}, entry.Metadata["old"])
	require.NotContains(t, entry.Metadata, "event_id")
}

func TestAudit_ApplyFailures(t *testing.T) {
	svc, repo, ledger, _, rec := audited()
	sub := Subscription{ProviderSubscriptionID: "sub_x", UserID: payer, Status: StatusActive, ProviderUpdatedAt: at(1)}

	rec.err = errAudit
	require.ErrorIs(t, svc.Apply(context.Background(), sub), errAudit)

	rec.err = nil
	repo.liveErr = errAudit
	require.ErrorIs(t, svc.Apply(context.Background(), sub), errAudit)
	repo.liveErr = nil
	ledger.compErr = errAudit
	require.ErrorIs(t, svc.Apply(context.Background(), sub), errAudit)
	require.Empty(t, rec.entries)
}

func TestAudit_Checkout(t *testing.T) {
	svc, repo, _, provider, rec := audited()
	repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Email: "pat@example.com"}}
	provider.customerID = "ctm_1"
	provider.checkoutID = "txn_1"
	ctx := claimCtx(payer)

	_, err := svc.Checkout(ctx, payer, Items{Seats: 2, StoragePacks: 1, Interval: "month"})
	require.NoError(t, err)
	require.Len(t, rec.entries, 1)
	entry := rec.entries[0]
	require.Equal(t, domain.AuditBillingCheckoutStarted, entry.Action)
	require.Empty(t, entry.ActorType)
	require.Equal(t, payer.Base36(), entry.TargetID)
	require.Equal(t, map[string]any{
		"seats": int64(2), "storage_packs": int64(1), "interval": IntervalMonth,
		"transaction_id": "txn_1", "customer_id": "ctm_1",
	}, entry.Metadata)

	rec.err = errAudit
	_, err = svc.Checkout(ctx, payer, Items{Seats: 2, Interval: "month"})
	require.ErrorIs(t, err, errAudit, "a failed record fails the checkout")

	provider.checkoutErr = errors.New("paddle down")
	_, err = svc.Checkout(ctx, payer, Items{Seats: 2, Interval: "month"})
	require.EqualError(t, err, "paddle down")
}

func TestAudit_ChangeSubscription(t *testing.T) {
	svc, repo, _, provider, rec := audited()
	repo.live = liveSub()
	provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Interval: IntervalMonth, Seats: 6, StoragePacks: 3, ProviderUpdatedAt: at(2)}

	_, err := svc.ChangeSubscription(claimCtx(payer), payer, Items{Seats: 6, StoragePacks: 2, Interval: "month"})
	require.NoError(t, err)
	require.Equal(t, []string{domain.AuditBillingSubscriptionApplied, domain.AuditBillingSubscriptionChanged}, rec.actions())
	changed := rec.entries[1]
	require.True(t, rec.inTx[1])
	require.Equal(t, "sub_live", changed.TargetID)
	require.Equal(t, map[string]any{"seats": int64(4), "storage_packs": int64(1), "interval": IntervalYear}, changed.Metadata["old"])
	require.Equal(t, map[string]any{"seats": int64(6), "storage_packs": int64(3), "interval": IntervalMonth}, changed.Metadata["new"], "new comes from the provider's answer, not the request")
	require.Empty(t, rec.entries[0].ActorType, "the user who changed the plan is the actor of the apply")

	rec.err = errAudit
	_, err = svc.ChangeSubscription(claimCtx(payer), payer, Items{Seats: 6, Interval: "month"})
	require.ErrorIs(t, err, errAudit)
}

func TestAudit_Sync(t *testing.T) {
	svc, repo, _, provider, rec := audited()
	repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, ProviderCustomerID: "ctm_1"}}
	provider.listed = []Subscription{{ProviderSubscriptionID: "sub_1", Status: StatusActive, Seats: 2, ProviderUpdatedAt: at(1)}}

	count, err := svc.Sync(claimCtx(1), payer)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, []string{domain.AuditBillingSubscriptionApplied, domain.AuditBillingSync}, rec.actions())
	require.Equal(t, map[string]any{"synced": 1}, rec.entries[1].Metadata)
	require.Equal(t, payer.Base36(), rec.entries[1].TargetID)

	repo.stale = true
	rec.entries = nil
	rec.err = errAudit
	_, err = svc.Sync(claimCtx(1), payer)
	require.ErrorIs(t, err, errAudit)
}

func TestAudit_Replay(t *testing.T) {
	svc, repo, _, provider := eventService(payer, "")
	rec := &captureRecorder{}
	svc.WithAudit(rec)
	provider.carriedErr = domain.NewErrorUser("unmappable")
	ctx := claimCtx(1)

	require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")))
	_, err := svc.Replay(ctx, "evt_1")
	require.NoError(t, err)
	require.Equal(t, []string{domain.AuditBillingEventReplayed}, rec.actions())
	require.Equal(t, map[string]any{
		"event_id": "evt_1", "event_type": "subscription.activated", "outcome": "failed", "error": "unmappable",
	}, rec.entries[0].Metadata)
	require.Equal(t, domain.AuditTargetBillingEvent, rec.entries[0].TargetType)

	provider.carriedErr = nil
	rec.entries = nil
	_, err = svc.Replay(ctx, "evt_1")
	require.NoError(t, err)
	require.Equal(t, []string{domain.AuditBillingSubscriptionApplied, domain.AuditBillingEventReplayed}, rec.actions())
	require.Equal(t, "evt_1", rec.entries[0].Metadata["event_id"])
	require.Empty(t, rec.entries[0].ActorType, "the replaying admin is the actor")
	require.Equal(t, "processed", rec.entries[1].Metadata["outcome"])

	repo.stale = true
	rec.err = errAudit
	_, err = svc.Replay(ctx, "evt_1")
	require.ErrorIs(t, err, errAudit)
}

func TestAudit_SeatDenied(t *testing.T) {
	svc, repo, _, _, rec := audited()
	repo.seats = 3
	ctx := claimCtx(5)

	err := svc.Admit(ctx, 1, 2, func(context.Context) error { return nil })
	require.Equal(t, 402, errorCode(err))
	require.Len(t, rec.entries, 1)
	entry := rec.entries[0]
	require.False(t, rec.inTx[0], "recorded after the gate transaction")
	require.Equal(t, domain.AuditBillingSeatDenied, entry.Action)
	require.Equal(t, snow.ID(1), *entry.OrgID)
	require.Equal(t, snow.ID(2).Base36(), entry.TargetID)
	require.Equal(t, map[string]any{"billing_owner_id": snow.ID(7).Base36(), "used": int64(3), "limit": int64(3)}, entry.Metadata)

	rec.err = errAudit
	err = svc.Admit(ctx, 1, 2, func(context.Context) error { return nil })
	require.Equal(t, 402, errorCode(err), "a failed record keeps the refusal")

	rec.err = nil
	rec.entries = nil
	repo.seats = 1
	require.NoError(t, svc.Admit(ctx, 1, 2, func(context.Context) error { return nil }))
	require.Empty(t, rec.entries, "admissions are recorded by the membership action")
}

func TestAudit_NilRecorder(t *testing.T) {
	svc, repo, _, provider := newProviderService()
	repo.seats = 3
	require.Equal(t, 402, errorCode(svc.Admit(context.Background(), 1, 2, func(context.Context) error { return nil })))
	repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, ProviderCustomerID: "ctm_1"}}
	provider.listed = []Subscription{{ProviderSubscriptionID: "sub_1", Status: StatusActive, ProviderUpdatedAt: at(1)}}
	_, err := svc.Sync(context.Background(), payer)
	require.NoError(t, err)
	require.Len(t, repo.upserted, 1)
}
