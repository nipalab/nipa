package billing

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (s *billingSuite) TestAdmit_AllEntryPointsAtTheLimit() {
	ctx := context.Background()
	owner := s.seedUser("owner")
	org := s.seedOrg(owner)
	group := s.seedGroup(org)
	orgs := postgres.NewOrgRepository(s.db)
	groups := postgres.NewGroupRepository(s.db)
	pbac := postgres.NewPBACRepository(s.db)

	member := s.seedUser("member")
	s.NoError(s.service.Admit(ctx, org, member, func(ctx context.Context) error {
		return orgs.UpsertMember(ctx, org, member, domain.OrgRoleMember)
	}))
	grouped := s.seedUser("grouped")
	s.NoError(s.service.Admit(ctx, org, grouped, func(ctx context.Context) error {
		return groups.AddMember(ctx, group, grouped)
	}))
	s.Equal(int64(3), s.seats(owner))

	ruled := s.seedUser("ruled")
	err := s.service.Admit(ctx, org, ruled, func(ctx context.Context) error {
		_, err := pbac.CreateRule(ctx, domain.PBACRule{UserID: &ruled, OrgID: org, Permission: domain.PermissionRead})
		return err
	})
	s.Equal(402, errorCode(err))
	s.Equal(int64(3), s.seats(owner))

	s.NoError(s.service.Admit(ctx, org, grouped, func(ctx context.Context) error {
		_, err := pbac.CreateRule(ctx, domain.PBACRule{UserID: &grouped, OrgID: org, Permission: domain.PermissionRead})
		return err
	}), "an existing seat is admitted at the limit")
}

func (s *billingSuite) TestAdmit_InsertRunsInTheGateTransaction() {
	ctx := context.Background()
	owner := s.seedUser("owner")
	org := s.seedOrg(owner)
	group := s.seedGroup(org)
	user := s.seedUser("rolled-back")
	orgs := postgres.NewOrgRepository(s.db)
	groups := postgres.NewGroupRepository(s.db)
	pbac := postgres.NewPBACRepository(s.db)
	boom := errors.New("boom")

	err := s.service.Admit(ctx, org, user, func(ctx context.Context) error {
		if err := orgs.UpsertMember(ctx, org, user, domain.OrgRoleMember); err != nil {
			return err
		}
		if err := groups.AddMember(ctx, group, user); err != nil {
			return err
		}
		if _, err := pbac.CreateRule(ctx, domain.PBACRule{UserID: &user, OrgID: org, Permission: domain.PermissionRead}); err != nil {
			return err
		}
		return boom
	})
	s.ErrorIs(err, boom)
	s.Equal(int64(1), s.seats(owner), "every repository insert rolled back with the gate transaction")
}

func (s *billingSuite) TestAdmit_ConcurrentLastSeat() {
	ctx := context.Background()
	owner := s.seedUser("owner")
	org := s.seedOrg(owner)
	s.exec(`INSERT INTO org_members (org_id, user_id) VALUES ($1, $2)`, org.Int64(), s.seedUser("second").Int64())
	orgs := postgres.NewOrgRepository(s.db)

	const n = 8
	users := make([]snow.ID, n)
	for i := range users {
		users[i] = s.seedUser("racer")
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.service.Admit(ctx, org, users[i], func(ctx context.Context) error {
				return orgs.UpsertMember(ctx, org, users[i], domain.OrgRoleMember)
			})
		}(i)
	}
	wg.Wait()

	admitted := 0
	for _, err := range errs {
		if err == nil {
			admitted++
			continue
		}
		s.Equal(402, errorCode(err), "unexpected error: %v", err)
	}
	s.Equal(1, admitted, "exactly one racer takes the last free seat")
	s.Equal(int64(3), s.seats(owner))
}

func (s *billingSuite) TestApply_OrderingAndQuota() {
	ctx := context.Background()
	owner := s.seedUser("owner")

	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_old", UserID: owner, Status: StatusActive, Seats: 2, ProviderUpdatedAt: at(1),
	}))
	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_new", UserID: owner, Status: StatusActive, Seats: 3, StoragePacks: 1, ProviderUpdatedAt: at(2),
	}))
	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_old", UserID: owner, Status: StatusCanceled, Seats: 2, ProviderUpdatedAt: at(9),
	}))
	quota, _ := s.quota(owner)
	s.Equal(int64(1000+3*100+5000), quota.Int64, "a late cancel of the old subscription keeps the newer live one")

	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_new", UserID: owner, Status: StatusCanceled, ProviderUpdatedAt: at(1),
	}))
	quota, _ = s.quota(owner)
	s.Equal(int64(1000+3*100+5000), quota.Int64, "an older event is ignored")

	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_new", UserID: owner, Status: StatusCanceled, ProviderUpdatedAt: at(10),
	}))
	quota, _ = s.quota(owner)
	s.Equal(testConfig.FreeQuotaBytes, quota.Int64, "no live subscription left: free quota")
}

func (s *billingSuite) TestApply_KeepsOverride() {
	ctx := context.Background()
	owner := s.seedUser("owner")
	override := int64(42)
	s.Require().NoError(s.ledger.SetQuotaOverride(ctx, owner, &override))

	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_1", UserID: owner, Status: StatusPastDue, Seats: 1, StoragePacks: 1, ProviderUpdatedAt: at(1),
	}))
	quota, stored := s.quota(owner)
	s.Equal(int64(1000+100+5000), quota.Int64)
	s.True(stored.Valid)
	s.Equal(int64(42), stored.Int64, "billing never touches the admin override")

	summary, err := s.ledger.OwnerStorage(ctx, owner)
	s.Require().NoError(err)
	s.Require().NotNil(summary.QuotaBytes)
	s.Equal(int64(42), *summary.QuotaBytes)
}

func (s *billingSuite) TestHandleEvent_DedupAndCustomerFallback() {
	ctx := context.Background()
	owner := s.seedUser("owner")
	provider := &stubProvider{carried: &EventSubscription{
		Subscription: Subscription{ProviderSubscriptionID: "sub_pg", Status: StatusActive, Seats: 5, ProviderUpdatedAt: at(1)},
		CustomerID:   "ctm_pg",
	}}
	s.service.WithProvider(provider)
	event := Event{ID: "evt_pg", Type: "subscription.activated", OccurredAt: at(0), Payload: []byte(`{"id":"evt_pg","type":"subscription.activated"}`)}

	s.NoError(s.service.HandleEvent(ctx, event), "an unknown customer is a permanent failure")
	stored, err := s.repo.Event(ctx, "evt_pg")
	s.Require().NoError(err)
	s.Equal("event customer has no billing account", stored.Error)

	s.Require().NoError(s.repo.InsertAccount(ctx, domain.BillingAccount{UserID: owner, Provider: "stub", ProviderCustomerID: "ctm_pg"}))
	s.NoError(s.service.HandleEvent(ctx, event))
	entitlement, err := s.service.Entitlement(ctx, owner)
	s.Require().NoError(err)
	s.False(entitlement.Paid, "a failed delivery is not re-run on redelivery")

	stored, err = s.service.Replay(ctx, "evt_pg")
	s.Require().NoError(err)
	s.NotNil(stored.ProcessedAt)
	s.Empty(stored.Error)
	entitlement, err = s.service.Entitlement(ctx, owner)
	s.Require().NoError(err)
	s.True(entitlement.Paid)
	s.Equal(int64(5)+testConfig.FreeSeats, entitlement.SeatLimit)
	quota, _ := s.quota(owner)
	s.Equal(testConfig.Entitlement(&Subscription{Status: StatusActive, Seats: 5}).QuotaBytes, quota.Int64)
}

type failingAudit struct {
	*usecase.Audit
}

func (f failingAudit) Record(ctx context.Context, entry domain.AuditEntry) error {
	if err := f.Audit.Record(ctx, entry); err != nil {
		return err
	}
	return errors.New("audit down")
}

func (s *billingSuite) newAudit() *usecase.Audit {
	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	return usecase.NewAudit(postgres.NewAuditRepository(s.db), node, dbtx.NewTransactor(s.db))
}

func (s *billingSuite) auditEntries(action string) []*domain.AuditEntry {
	s.T().Helper()
	entries, err := postgres.NewAuditRepository(s.db).List(context.Background(), domain.AuditFilter{ActionPrefix: action, Limit: 100})
	s.Require().NoError(err)
	return entries
}

func (s *billingSuite) TestAudit_ApplyCommitsAndRollsBackWithTheChange() {
	ctx := context.Background()
	owner := s.seedUser("owner")
	s.service.WithProvider(&stubProvider{})
	sub := Subscription{ProviderSubscriptionID: "sub_a", UserID: owner, Status: StatusActive, Seats: 2, ProviderUpdatedAt: at(1)}

	s.service.WithAudit(failingAudit{s.newAudit()})
	s.Error(s.service.Apply(ctx, sub))
	live, err := s.repo.LiveSubscription(ctx, owner)
	s.Require().NoError(err)
	s.Nil(live, "the subscription rolled back with its entry")
	s.Empty(s.auditEntries("billing."))

	s.service.WithAudit(s.newAudit())
	s.Require().NoError(s.service.Apply(ctx, sub))
	s.Require().NoError(s.service.Apply(ctx, Subscription{ProviderSubscriptionID: "sub_a", UserID: owner, Status: StatusCanceled, ProviderUpdatedAt: at(0)}))
	entries := s.auditEntries(domain.AuditBillingSubscriptionApplied)
	s.Require().Len(entries, 1, "an older update records nothing")
	s.Equal(domain.AuditActorProvider, entries[0].ActorType)
	s.Equal("stub", entries[0].ActorLabel)
	s.Nil(entries[0].ActorUserID)
	newMeta := entries[0].Metadata["new"].(map[string]any)
	s.EqualValues(testConfig.Entitlement(&sub).QuotaBytes, newMeta["quota_bytes"])
	s.Equal(StatusActive, newMeta["status"])
}

func (s *billingSuite) TestAudit_SeatDeniedSurvivesTheRollback() {
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 77})
	owner := s.seedUser("owner")
	org := s.seedOrg(owner)
	orgs := postgres.NewOrgRepository(s.db)
	for range testConfig.FreeSeats - 1 {
		s.exec(`INSERT INTO org_members (org_id, user_id) VALUES ($1, $2)`, org.Int64(), s.seedUser("member").Int64())
	}
	audit := s.newAudit()
	s.service.WithAudit(audit)
	outsider := s.seedUser("outsider")

	err := audit.WithinTx(ctx, func(ctx context.Context) error {
		return s.service.Admit(ctx, org, outsider, func(ctx context.Context) error {
			return orgs.UpsertMember(ctx, org, outsider, domain.OrgRoleMember)
		})
	})
	s.Equal(402, errorCode(err))
	_, err = orgs.MemberRole(ctx, org, outsider)
	s.True(domain.IsErrorNotFound(err), "the membership was not written: %v", err)
	entries := s.auditEntries(domain.AuditBillingSeatDenied)
	s.Require().Len(entries, 1, "the refusal is kept although the caller's transaction rolled back")
	s.Equal(snow.ID(77), *entries[0].ActorUserID)
	s.Equal(org, *entries[0].OrgID)
	s.Equal(outsider.Base36(), entries[0].TargetID)
	s.Equal(owner.Base36(), entries[0].Metadata["billing_owner_id"])
	s.EqualValues(testConfig.FreeSeats, entries[0].Metadata["used"])
	s.EqualValues(testConfig.FreeSeats, entries[0].Metadata["limit"])
}

func (s *billingSuite) TestReconcileQuotas_AppliesNewSettingsToEveryOwner() {
	ctx := context.Background()
	free := s.seedUser("free")
	payer := s.seedUser("payer")
	overridden := s.seedUser("overridden")
	current := s.seedUser("current")
	deleted := s.seedUser("deleted")
	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_payer", UserID: payer, Status: StatusActive, Seats: 3, StoragePacks: 1, ProviderUpdatedAt: at(1),
	}))
	s.exec(`INSERT INTO owner_quota (user_id, quota_bytes, override_bytes) VALUES ($1, 1000, NULL), ($2, 1000, 777), ($3, 4000, NULL), ($4, 1000, NULL)`,
		free.Int64(), overridden.Int64(), current.Int64(), deleted.Int64())
	s.exec(`UPDATE users SET deleted = true WHERE id = $1`, deleted.Int64())
	s.exec(`UPDATE owner_quota SET updated_at = '2020-01-01' WHERE user_id = $1`, current.Int64())

	cfg := Config{FreeSeats: 3, FreeQuotaBytes: 4000, SeatIncludedBytes: 200, StoragePackBytes: 5000}
	rec := &captureRecorder{}
	svc := New(s.repo, s.ledger, dbtx.NewTransactor(s.db), cfg).WithAudit(rec)

	updated, err := svc.ReconcileQuotas(ctx)
	s.Require().NoError(err)
	s.Equal(3, updated, "free, payer and overridden; current matches and deleted is skipped")

	quota, _ := s.quota(free)
	s.Equal(int64(4000), quota.Int64)
	quota, _ = s.quota(payer)
	s.Equal(int64(4000+3*200+5000), quota.Int64)
	quota, override := s.quota(overridden)
	s.Equal(int64(4000), quota.Int64)
	s.Equal(sql.NullInt64{Int64: 777, Valid: true}, override, "the admin override is untouched")
	quota, _ = s.quota(deleted)
	s.Equal(int64(1000), quota.Int64)
	var touched time.Time
	s.Require().NoError(s.db.QueryRowContext(ctx, `SELECT updated_at FROM owner_quota WHERE user_id = $1`, current.Int64()).Scan(&touched))
	s.Equal(2020, touched.Year(), "a correct row is not rewritten")
	s.Equal([]string{domain.AuditBillingQuotasReconciled}, rec.actions())

	updated, err = svc.ReconcileQuotas(ctx)
	s.Require().NoError(err)
	s.Zero(updated, "a second run finds nothing to do")
	s.Len(rec.entries, 1)
}
