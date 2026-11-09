package postgres

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

var billingIDCounter atomic.Int64

type BillingRepositorySuite struct {
	baseSuite
	repo *BillingRepository
}

func TestBillingRepositorySuite(t *testing.T) {
	suite.Run(t, new(BillingRepositorySuite))
}

func (s *BillingRepositorySuite) SetupTest() {
	s.baseSuite.SetupTest()
	s.repo = NewBillingRepository(s.db)
}

func (s *BillingRepositorySuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(), query, args...)
	s.Require().NoError(err)
}

func (s *BillingRepositorySuite) nextID() snow.ID {
	return snow.ID(billingIDCounter.Add(1) + 500000)
}

func (s *BillingRepositorySuite) user(name string) snow.ID {
	s.T().Helper()
	id := s.nextID()
	s.exec(`INSERT INTO users (id, name, email, password) VALUES ($1, $2, $3, 'x')`,
		id.Int64(), name, fmt.Sprintf("%s-%d@example.com", name, id.Int64()))
	return id
}

func (s *BillingRepositorySuite) org(createdBy snow.ID) snow.ID {
	s.T().Helper()
	id := s.nextID()
	s.exec(`INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $2, $3)`,
		id.Int64(), fmt.Sprintf("billing-org-%d", id.Int64()), createdBy.Int64())
	s.member(id, createdBy)
	return id
}

func (s *BillingRepositorySuite) member(orgID, userID snow.ID) {
	s.T().Helper()
	s.exec(`INSERT INTO org_members (org_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, orgID.Int64(), userID.Int64())
}

func (s *BillingRepositorySuite) group(orgID snow.ID, members ...snow.ID) snow.ID {
	s.T().Helper()
	id := s.nextID()
	s.exec(`INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`, id.Int64(), orgID.Int64(), fmt.Sprintf("g-%d", id.Int64()))
	for _, member := range members {
		s.exec(`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, id.Int64(), member.Int64())
	}
	return id
}

func (s *BillingRepositorySuite) rule(orgID, userID snow.ID, projectID *snow.ID) {
	s.T().Helper()
	s.exec(`INSERT INTO pbac_rules (user_id, org_id, project_id, permission) VALUES ($1, $2, $3, 1)`,
		userID.Int64(), orgID.Int64(), nullSnowID(projectID))
}

func (s *BillingRepositorySuite) seats(owner snow.ID) int64 {
	s.T().Helper()
	seats, err := s.repo.CountSeats(context.Background(), owner)
	s.Require().NoError(err)
	return seats
}

func (s *BillingRepositorySuite) isSeat(owner, user snow.ID) bool {
	s.T().Helper()
	seat, err := s.repo.IsSeat(context.Background(), owner, user)
	s.Require().NoError(err)
	return seat
}

func (s *BillingRepositorySuite) TestSeats_AllAccessPathsAcrossOrgs() {
	owner := s.user("owner")
	orgA := s.org(owner)
	orgB := s.org(owner)
	s.Equal(int64(1), s.seats(owner), "the owner is a seat")

	member := s.user("member")
	s.member(orgA, member)
	s.member(orgB, member)
	grouped := s.user("grouped")
	s.group(orgB, grouped, member)
	ruled := s.user("ruled")
	project := seedProject(s.T(), s.q, orgA.Int64(), fmt.Sprintf("billing-project-%d", s.nextID().Int64()))
	s.rule(orgA, ruled, &project)
	orgRuled := s.user("org-ruled")
	s.rule(orgB, orgRuled, nil)
	s.rule(orgB, member, nil)

	s.Equal(int64(5), s.seats(owner), "every user counts once across paths and orgs")
	for _, user := range []snow.ID{owner, member, grouped, ruled, orgRuled} {
		s.True(s.isSeat(owner, user))
	}

	stranger := s.user("stranger")
	s.False(s.isSeat(owner, stranger))
	otherOwner := s.user("other-owner")
	s.member(s.org(otherOwner), stranger)
	s.Equal(int64(5), s.seats(owner), "another owner's org does not count")
	s.Equal(int64(2), s.seats(otherOwner))
	s.False(s.isSeat(owner, stranger))
}

func (s *BillingRepositorySuite) TestSeats_ExcludeDeleted() {
	owner := s.user("owner")
	org := s.org(owner)

	gone := s.user("gone")
	s.member(org, gone)
	s.exec(`UPDATE users SET deleted = true, deleted_at = now() WHERE id = $1`, gone.Int64())

	grouped := s.user("in-deleted-group")
	group := s.group(org, grouped)
	s.exec(`UPDATE groups SET deleted = true, deleted_at = now() WHERE id = $1`, group.Int64())

	deletedOrg := s.org(owner)
	inDeletedOrg := s.user("in-deleted-org")
	s.member(deletedOrg, inDeletedOrg)
	s.exec(`UPDATE organizations SET deleted = true, deleted_at = now() WHERE id = $1`, deletedOrg.Int64())

	s.Equal(int64(1), s.seats(owner))
	for _, user := range []snow.ID{gone, grouped, inDeletedOrg} {
		s.False(s.isSeat(owner, user))
	}
}

func (s *BillingRepositorySuite) TestSeats_FollowBillingOwnerMapping() {
	creator := s.user("creator")
	payer := s.user("payer")
	org := s.org(creator)
	s.member(org, s.user("member"))

	s.Equal(int64(2), s.seats(creator), "unmapped orgs fall back to the creator")
	s.exec(`INSERT INTO org_billing_owner (org_id, user_id) VALUES ($1, $2)`, org.Int64(), payer.Int64())
	s.Equal(int64(1), s.seats(creator))
	s.Equal(int64(3), s.seats(payer), "the payer, the creator and the member")
	s.True(s.isSeat(payer, payer))
}

func (s *BillingRepositorySuite) TestSubscriptions_NewerWinsAndLiveSelection() {
	ctx := context.Background()
	owner := s.user("owner")
	at := func(minute int) time.Time { return time.Date(2026, 11, 7, 12, minute, 0, 0, time.UTC) }

	live, err := s.repo.LiveSubscription(ctx, owner)
	s.Require().NoError(err)
	s.Nil(live)

	end := at(30)
	written, err := s.repo.UpsertSubscription(ctx, domain.BillingSubscription{
		ProviderSubscriptionID: "sub_a", UserID: owner, Plan: "team", Status: "active", Interval: "month",
		Seats: 4, StoragePacks: 1, CurrentPeriodEnd: &end, CancelAtPeriodEnd: true, ProviderUpdatedAt: at(5),
	})
	s.Require().NoError(err)
	s.True(written)

	written, err = s.repo.UpsertSubscription(ctx, domain.BillingSubscription{
		ProviderSubscriptionID: "sub_a", UserID: owner, Status: "canceled", ProviderUpdatedAt: at(4),
	})
	s.Require().NoError(err)
	s.False(written, "an older update is ignored")
	written, err = s.repo.UpsertSubscription(ctx, domain.BillingSubscription{
		ProviderSubscriptionID: "sub_a", UserID: owner, Status: "canceled", ProviderUpdatedAt: at(5),
	})
	s.Require().NoError(err)
	s.False(written, "a replay of the same update is ignored")

	other := s.user("other")
	written, err = s.repo.UpsertSubscription(ctx, domain.BillingSubscription{
		ProviderSubscriptionID: "sub_a", UserID: other, Status: "active", ProviderUpdatedAt: at(5).Add(time.Minute),
	})
	s.Require().NoError(err)
	s.False(written, "a subscription never moves to another user")
	moved, err := s.repo.LiveSubscription(ctx, other)
	s.Require().NoError(err)
	s.Nil(moved)

	live, err = s.repo.LiveSubscription(ctx, owner)
	s.Require().NoError(err)
	s.Require().NotNil(live)
	s.Equal("sub_a", live.ProviderSubscriptionID)
	s.Equal(owner, live.UserID)
	s.Equal("team", live.Plan)
	s.Equal("active", live.Status)
	s.Equal("month", live.Interval)
	s.Equal(int64(4), live.Seats)
	s.Equal(int64(1), live.StoragePacks)
	s.True(live.CancelAtPeriodEnd)
	s.Require().NotNil(live.CurrentPeriodEnd)
	s.True(end.Equal(*live.CurrentPeriodEnd))
	s.True(at(5).Equal(live.ProviderUpdatedAt))

	_, err = s.repo.UpsertSubscription(ctx, domain.BillingSubscription{
		ProviderSubscriptionID: "sub_b", UserID: owner, Status: "past_due", ProviderUpdatedAt: at(6),
	})
	s.Require().NoError(err)
	live, err = s.repo.LiveSubscription(ctx, owner)
	s.Require().NoError(err)
	s.Equal("sub_b", live.ProviderSubscriptionID, "the newest live subscription wins")

	for _, id := range []string{"sub_a", "sub_b"} {
		written, err = s.repo.UpsertSubscription(ctx, domain.BillingSubscription{
			ProviderSubscriptionID: id, UserID: owner, Status: "canceled", ProviderUpdatedAt: at(20),
		})
		s.Require().NoError(err)
		s.True(written)
	}
	live, err = s.repo.LiveSubscription(ctx, owner)
	s.Require().NoError(err)
	s.Nil(live, "canceled subscriptions are not live")
}

func (s *BillingRepositorySuite) TestLockOwner_SerializesPerOwner() {
	ctx := context.Background()
	owner := s.user("owner")
	other := s.user("other")
	transactor := dbtx.NewTransactor(s.db)

	tx, err := s.db.BeginTx(ctx, nil)
	s.Require().NoError(err)
	s.Require().NoError(s.repo.LockOwner(dbtx.WithTx(ctx, tx), owner))

	blocked, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	s.Error(transactor.WithinTx(blocked, func(ctx context.Context) error {
		return s.repo.LockOwner(ctx, owner)
	}), "a second holder waits for the owner lock")
	s.NoError(transactor.WithinTx(ctx, func(ctx context.Context) error {
		return s.repo.LockOwner(ctx, other)
	}), "other owners are not blocked")

	s.Require().NoError(tx.Rollback())
	s.NoError(transactor.WithinTx(ctx, func(ctx context.Context) error {
		return s.repo.LockOwner(ctx, owner)
	}))
}

func (s *BillingRepositorySuite) TestDatabaseErrors() {
	ctx := context.Background()
	owner := s.user("owner")
	s.exec(`DROP TABLE billing_subscriptions`)
	s.exec(`DROP TABLE group_members CASCADE`)

	_, err := s.repo.CountSeats(ctx, owner)
	requireDatabaseError(s.T(), err)
	_, err = s.repo.IsSeat(ctx, owner, owner)
	requireDatabaseError(s.T(), err)
	_, err = s.repo.LiveSubscription(ctx, owner)
	requireDatabaseError(s.T(), err)
	_, err = s.repo.UpsertSubscription(ctx, domain.BillingSubscription{ProviderSubscriptionID: "x", UserID: owner, ProviderUpdatedAt: time.Now()})
	requireDatabaseError(s.T(), err)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	requireDatabaseError(s.T(), s.repo.LockOwner(canceled, owner))
}

func requireDatabaseError(t *testing.T, err error) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func (s *BillingRepositorySuite) TestAccounts() {
	ctx := context.Background()
	owner := s.user("owner")
	other := s.user("other")

	account, err := s.repo.Account(ctx, owner)
	s.Require().NoError(err)
	s.Nil(account)

	s.Require().NoError(s.repo.InsertAccount(ctx, domain.BillingAccount{UserID: owner, Provider: "paddle", ProviderCustomerID: "ctm_1"}))
	s.Require().NoError(s.repo.InsertAccount(ctx, domain.BillingAccount{UserID: owner, Provider: "paddle", ProviderCustomerID: "ctm_2"}),
		"a second customer for the same user is ignored")
	s.Require().NoError(s.repo.InsertAccount(ctx, domain.BillingAccount{UserID: other, Provider: "paddle", ProviderCustomerID: "ctm_1"}),
		"a customer linked to another user is ignored")

	account, err = s.repo.Account(ctx, owner)
	s.Require().NoError(err)
	s.Equal(&domain.BillingAccount{UserID: owner, Provider: "paddle", ProviderCustomerID: "ctm_1"}, account)
	account, err = s.repo.Account(ctx, other)
	s.Require().NoError(err)
	s.Nil(account)

	account, err = s.repo.AccountByCustomer(ctx, "paddle", "ctm_1")
	s.Require().NoError(err)
	s.Require().NotNil(account)
	s.Equal(owner, account.UserID)
	account, err = s.repo.AccountByCustomer(ctx, "paddle", "ctm_missing")
	s.Require().NoError(err)
	s.Nil(account)
}

func (s *BillingRepositorySuite) TestUser() {
	ctx := context.Background()
	id := s.user("payer")
	s.exec(`UPDATE users SET deleted = true WHERE id = $1`, id.Int64())

	user, err := s.repo.User(ctx, id)
	s.Require().NoError(err)
	s.Equal(id, user.ID)
	s.Equal("payer", user.Name)
	s.Contains(user.Email, "payer-")
	s.True(user.Deleted)

	_, err = s.repo.User(ctx, s.nextID())
	s.True(domain.IsErrorNotFound(err))
}

func (s *BillingRepositorySuite) TestEvents() {
	ctx := context.Background()
	occurred := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	event := domain.BillingEvent{EventID: "evt_1", Type: "subscription.activated", OccurredAt: occurred, Payload: []byte(`{"a":1}`)}

	inserted, err := s.repo.InsertEvent(ctx, event)
	s.Require().NoError(err)
	s.True(inserted)
	inserted, err = s.repo.InsertEvent(ctx, event)
	s.Require().NoError(err)
	s.False(inserted, "a duplicate delivery is not stored twice")

	stored, err := s.repo.Event(ctx, "evt_1")
	s.Require().NoError(err)
	s.Equal("subscription.activated", stored.Type)
	s.True(occurred.Equal(stored.OccurredAt))
	s.JSONEq(`{"a":1}`, string(stored.Payload))
	s.Nil(stored.ProcessedAt)
	s.Empty(stored.Error)

	s.Require().NoError(s.repo.MarkEventFailed(ctx, "evt_1", "unknown user"))
	stored, err = s.repo.Event(ctx, "evt_1")
	s.Require().NoError(err)
	s.Equal("unknown user", stored.Error)
	s.Nil(stored.ProcessedAt)

	s.Require().NoError(s.repo.MarkEventProcessed(ctx, "evt_1"))
	stored, err = s.repo.Event(ctx, "evt_1")
	s.Require().NoError(err)
	s.NotNil(stored.ProcessedAt)
	s.Empty(stored.Error, "a successful replay clears the error")

	_, err = s.repo.Event(ctx, "evt_missing")
	s.True(domain.IsErrorNotFound(err))
}

func (s *BillingRepositorySuite) TestQuotaOwners_KeysetPages() {
	ctx := context.Background()
	var owners []snow.ID
	for i := 0; i < 5; i++ {
		owner := s.user("quota")
		owners = append(owners, owner)
		s.exec(`INSERT INTO owner_quota (user_id, quota_bytes) VALUES ($1, 10)`, owner.Int64())
	}
	s.user("no-row")

	first, err := s.repo.QuotaOwners(ctx, 0, 2)
	s.Require().NoError(err)
	s.Equal(owners[:2], first)
	rest, err := s.repo.QuotaOwners(ctx, first[1], 10)
	s.Require().NoError(err)
	s.Equal(owners[2:], rest)
	none, err := s.repo.QuotaOwners(ctx, owners[4], 10)
	s.Require().NoError(err)
	s.Empty(none)

	s.exec(`DROP TABLE owner_quota`)
	_, err = s.repo.QuotaOwners(ctx, 0, 10)
	requireDatabaseError(s.T(), err)
}
