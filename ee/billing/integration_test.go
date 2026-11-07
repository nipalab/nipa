package billing

import (
	"context"
	"errors"
	"sync"

	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
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
	s.Equal(int64(3*100+5000), quota.Int64, "a late cancel of the old subscription keeps the newer live one")

	s.Require().NoError(s.service.Apply(ctx, Subscription{
		ProviderSubscriptionID: "sub_new", UserID: owner, Status: StatusCanceled, ProviderUpdatedAt: at(1),
	}))
	quota, _ = s.quota(owner)
	s.Equal(int64(3*100+5000), quota.Int64, "an older event is ignored")

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
	s.Equal(int64(5100), quota.Int64)
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
	s.Equal("event names no known user", stored.Error)

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
	s.Equal(int64(5), entitlement.SeatLimit)
	quota, _ := s.quota(owner)
	s.Equal(testConfig.Entitlement(&Subscription{Status: StatusActive, Seats: 5}).QuotaBytes, quota.Int64)
}
