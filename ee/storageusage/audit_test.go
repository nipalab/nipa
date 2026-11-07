package storageusage

import (
	"context"
	"errors"

	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

var errAudit = errors.New("audit down")

// failingRecorder stores the entry, then fails, so a test can see both the
// change and its entry roll back.
type failingRecorder struct {
	*usecase.Audit
}

func (f failingRecorder) Record(ctx context.Context, entry domain.AuditEntry) error {
	if err := f.Audit.Record(ctx, entry); err != nil {
		return err
	}
	return errAudit
}

func (s *ledgerSuite) newAudit() *usecase.Audit {
	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	return usecase.NewAudit(postgres.NewAuditRepository(s.db), node, dbtx.NewTransactor(s.db))
}

func (s *ledgerSuite) auditEntries(action string) []*domain.AuditEntry {
	s.T().Helper()
	entries, err := postgres.NewAuditRepository(s.db).List(context.Background(), domain.AuditFilter{ActionPrefix: action, Limit: 100})
	s.Require().NoError(err)
	return entries
}

func adminCtx() context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 77, IsSuperAdmin: true})
}

func (s *ledgerSuite) TestAudit_QuotaOverride() {
	ctx := adminCtx()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 100}).WithAudit(s.newAudit())
	owner := s.seedUser("audited-owner")

	first := int64(1000)
	s.Require().NoError(ledger.SetQuotaOverride(ctx, owner, &first))
	s.Require().NoError(ledger.SetQuotaOverride(ctx, owner, &first))
	s.Require().NoError(ledger.SetQuotaOverride(ctx, owner, nil))
	s.Require().NoError(ledger.ClearQuotaOverride(ctx, owner))
	s.Require().NoError(ledger.ClearQuotaOverride(ctx, owner))

	set := s.auditEntries(domain.AuditBillingQuotaOverrideSet)
	s.Require().Len(set, 2, "an unchanged override records nothing")
	s.Equal(domain.AuditActorUser, set[1].ActorType)
	s.Equal(snow.ID(77), *set[1].ActorUserID)
	s.Equal(owner.Base36(), set[1].TargetID)
	s.Nil(set[1].Metadata["old_override_bytes"])
	s.EqualValues(1000, set[1].Metadata["new_override_bytes"])
	s.EqualValues(1000, set[0].Metadata["old_override_bytes"])
	s.EqualValues(0, set[0].Metadata["new_override_bytes"], "a nil override is stored as unlimited")

	cleared := s.auditEntries(domain.AuditBillingQuotaOverrideCleared)
	s.Require().Len(cleared, 1, "clearing without an override records nothing")
	s.EqualValues(0, cleared[0].Metadata["old_override_bytes"])
}

func (s *ledgerSuite) TestAudit_QuotaOverrideRollsBackWithTheRecord() {
	ctx := adminCtx()
	ledger := NewLedger(s.db, Config{}).WithAudit(failingRecorder{s.newAudit()})
	owner := s.seedUser("rollback-owner")
	quota := int64(5)

	s.ErrorIs(ledger.SetQuotaOverride(ctx, owner, &quota), errAudit)
	summary, err := ledger.OwnerStorage(ctx, owner)
	s.Require().NoError(err)
	s.Nil(summary.QuotaBytes, "the override rolled back")
	s.Empty(s.auditEntries("billing."))

	s.Require().NoError(NewLedger(s.db, Config{}).SetQuotaOverride(ctx, owner, &quota))
	s.ErrorIs(ledger.ClearQuotaOverride(ctx, owner), errAudit)
	summary, err = ledger.OwnerStorage(ctx, owner)
	s.Require().NoError(err)
	s.Equal(int64(5), *summary.QuotaBytes, "the clear rolled back")
	s.Empty(s.auditEntries("billing."))
}

func (s *ledgerSuite) TestAudit_BillingOwnerTransfer() {
	ctx := adminCtx()
	ownerA := s.seedUser("transfer-a")
	ownerB := s.seedUser("transfer-b")
	org := s.seedOrg(ownerA)

	failing := NewLedger(s.db, Config{}).WithAudit(failingRecorder{s.newAudit()})
	s.ErrorIs(failing.SetBillingOwner(ctx, org, ownerB), errAudit)
	_, mapped := s.billingOwner(org)
	s.False(mapped, "the transfer and the lazy seed rolled back")
	s.Empty(s.auditEntries("billing."))

	ledger := NewLedger(s.db, Config{}).WithAudit(s.newAudit())
	s.Require().NoError(ledger.SetBillingOwner(ctx, org, ownerB))
	s.Require().NoError(ledger.SetBillingOwner(ctx, org, ownerB))
	entries := s.auditEntries(domain.AuditBillingOwnerTransferred)
	s.Require().Len(entries, 1, "a transfer to the current owner records nothing")
	s.Equal(org, *entries[0].OrgID)
	s.Equal(domain.AuditTargetOrg, entries[0].TargetType)
	s.Equal(ownerA.Base36(), entries[0].Metadata["old_owner_id"])
	s.Equal(ownerB.Base36(), entries[0].Metadata["new_owner_id"])
	mappedOwner, _ := s.billingOwner(org)
	s.Equal(ownerB, mappedOwner)
}

func (s *ledgerSuite) TestComputedQuota() {
	ctx := context.Background()
	owner := s.seedUser("computed")
	quota, err := s.ledger.ComputedQuota(ctx, owner)
	s.Require().NoError(err)
	s.Nil(quota, "no row")

	override := int64(3)
	s.Require().NoError(s.ledger.SetQuotaOverride(ctx, owner, &override))
	s.Require().NoError(s.ledger.SetComputedQuota(ctx, owner, 0))
	s.Require().NoError(s.ledger.SetComputedQuota(ctx, owner, 42))
	quota, err = s.ledger.ComputedQuota(ctx, owner)
	s.Require().NoError(err)
	s.Equal(int64(42), *quota)
}
