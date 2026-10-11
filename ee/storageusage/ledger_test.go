package storageusage

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (s *ledgerSuite) TestAttribute_LinksAndCountsExactlyOnce() {
	ctx := context.Background()
	org := snow.ID(1)
	project := snow.ID(1)
	hash, _ := s.seedChunk(100)

	chunk := usecase.LedgerChunk{Hash: hash, SizeBytes: 100}
	s.NoError(s.ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{chunk}))

	s.Equal(int64(100), s.orgUsed(org))
	s.Equal(int64(100), s.projectUsed(project))
	s.Equal(1, s.linkCount(project))

	sizes, err := s.ledger.AttributedSizes(ctx, project, []domain.Hash{hash, {99}})
	s.NoError(err)
	s.Equal(map[domain.Hash]int64{hash: 100}, sizes)

	s.NoError(s.ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{chunk}))
	s.Equal(int64(100), s.orgUsed(org))
	s.Equal(int64(100), s.projectUsed(project))
	s.Equal(1, s.linkCount(project))
}

func (s *ledgerSuite) TestAttribute_LargeBatch() {
	ctx := context.Background()
	const n = 300
	chunks := make([]usecase.LedgerChunk, 0, n)
	for i := 0; i < n; i++ {
		hash, _ := s.seedChunk(10)
		chunks = append(chunks, usecase.LedgerChunk{Hash: hash, SizeBytes: 10})
	}

	s.NoError(s.ledger.Attribute(ctx, &domain.Organization{ID: 1}, &domain.Project{ID: 1}, chunks))
	s.Equal(int64(n*10), s.orgUsed(snow.ID(1)))
	s.Equal(n, s.linkCount(snow.ID(1)))
}

func (s *ledgerSuite) TestAttribute_QuotaRejectsAndRollsBack() {
	ctx := context.Background()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 50})
	org := snow.ID(1)
	project := snow.ID(1)
	hash, _ := s.seedChunk(100)

	err := ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 100}})
	s.True(isQuotaExceeded(err), "expected a 402 quota error, got %v", err)
	s.Equal(0, s.linkCount(project))
	s.Equal(int64(0), s.orgUsed(org))
	s.Equal(int64(0), s.projectUsed(project))

	quota := int64(1000)
	s.NoError(ledger.SetOwnerQuota(ctx, snow.ID(1), &quota))
	s.NoError(ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 100}}))
	s.Equal(int64(100), s.orgUsed(org))
	s.Equal(int64(100), s.projectUsed(project))
}

func (s *ledgerSuite) TestAttribute_ProjectAndOrgShareOneQuota() {
	ctx := context.Background()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 150})
	org := snow.ID(1)
	projectA := snow.ID(1)
	projectB := s.seedProject(org)

	hashA, _ := s.seedChunk(100)
	hashB, _ := s.seedChunk(100)
	s.NoError(ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: projectA}, []usecase.LedgerChunk{{Hash: hashA, SizeBytes: 100}}))

	err := ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: projectB}, []usecase.LedgerChunk{{Hash: hashB, SizeBytes: 100}})
	s.True(isQuotaExceeded(err), "the owner's quota covers all their orgs and projects")
}

func (s *ledgerSuite) TestHeadroom_QuotaSemantics() {
	ctx := context.Background()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 100})
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: 1}

	s.NoError(ledger.Headroom(ctx, org, project, 50))
	s.True(isQuotaExceeded(ledger.Headroom(ctx, org, project, 150)))

	s.NoError(ledger.SetOwnerQuota(ctx, snow.ID(1), nil))
	s.NoError(ledger.Headroom(ctx, org, project, 1_000_000))

	zero := int64(0)
	s.NoError(ledger.SetOwnerQuota(ctx, snow.ID(1), &zero))
	s.NoError(ledger.Headroom(ctx, org, project, 1_000_000))

	small := int64(10)
	s.NoError(ledger.SetOwnerQuota(ctx, snow.ID(1), &small))
	s.True(isQuotaExceeded(ledger.Headroom(ctx, org, project, 11)))
}

func (s *ledgerSuite) TestBillingOwner_SeedsFromCreator() {
	ctx := context.Background()
	owner := s.seedUser("creator")
	org := s.seedOrg(owner)
	project := s.seedProject(org)
	hash, _ := s.seedChunk(10)

	_, seeded := s.billingOwner(org)
	s.False(seeded, "the mapping is seeded lazily")

	s.NoError(s.ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}}))

	mapped, ok := s.billingOwner(org)
	s.True(ok)
	s.Equal(owner, mapped)

	summary, err := s.ledger.OwnerStorage(ctx, owner)
	s.NoError(err)
	s.Equal(int64(10), summary.UsedBytes)
	s.Len(summary.Orgs, 1)
	s.Equal(int64(10), summary.Orgs[0].UsedBytes)
}

func (s *ledgerSuite) TestSetBillingOwner_TransfersAggregate() {
	ctx := context.Background()
	ownerA := s.seedUser("owner-a")
	ownerB := s.seedUser("owner-b")
	org := s.seedOrg(ownerA)
	project := s.seedProject(org)
	hash, _ := s.seedChunk(100)

	s.NoError(s.ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 100}}))
	s.NoError(s.ledger.SetBillingOwner(ctx, org, ownerB))

	summaryA, err := s.ledger.OwnerStorage(ctx, ownerA)
	s.NoError(err)
	s.Equal(int64(0), summaryA.UsedBytes)
	s.Empty(summaryA.Orgs)

	summaryB, err := s.ledger.OwnerStorage(ctx, ownerB)
	s.NoError(err)
	s.Equal(int64(100), summaryB.UsedBytes)
	s.Len(summaryB.Orgs, 1)
	s.Equal(int64(100), s.orgUsed(org))

	s.True(domain.IsErrorNotFound(s.ledger.SetBillingOwner(ctx, org, snow.ID(999999))))
}

func (s *ledgerSuite) TestSetOwnerQuota_RejectsNegative() {
	negative := int64(-1)
	err := s.ledger.SetOwnerQuota(context.Background(), snow.ID(1), &negative)
	s.Equal(400, errorCode(err))
}

func (s *ledgerSuite) TestOrgStorage_ReportsQuotaAndBreakdown() {
	ctx := context.Background()
	org := snow.ID(1)
	project := snow.ID(1)
	hash, _ := s.seedChunk(100)

	quota := int64(500)
	s.NoError(s.ledger.SetOwnerQuota(ctx, snow.ID(1), &quota))
	s.NoError(s.ledger.Attribute(ctx, &domain.Organization{ID: org}, &domain.Project{ID: project}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 100}}))

	summary, err := s.ledger.OrgStorage(ctx, org)
	s.NoError(err)
	s.Equal(int64(100), summary.UsedBytes)
	s.Require().NotNil(summary.QuotaBytes)
	s.Equal(int64(500), *summary.QuotaBytes)
	s.Equal(snow.ID(1), summary.OwnerID)
	s.Require().Len(summary.Projects, 1)
	s.Equal(project, summary.Projects[0].ProjectID)
	s.Equal(int64(100), summary.Projects[0].UsedBytes)

	s.NoError(s.ledger.SetOwnerQuota(ctx, snow.ID(1), nil))
	summary, err = s.ledger.OrgStorage(ctx, org)
	s.NoError(err)
	s.Nil(summary.QuotaBytes, "a cleared quota is unlimited")
}

func (s *ledgerSuite) TestAttributeProjectChunks_ResolvesOrg() {
	ctx := context.Background()
	hash, _ := s.seedChunk(50)

	s.NoError(s.ledger.AttributeProjectChunks(ctx, snow.ID(1), []usecase.LedgerChunk{{Hash: hash, SizeBytes: 50}}))
	s.Equal(int64(50), s.orgUsed(snow.ID(1)))
	s.Equal(int64(50), s.projectUsed(snow.ID(1)))
}

func (s *ledgerSuite) TestAttribute_MissingChunkMetadataErrors() {
	ctx := context.Background()
	var hash domain.Hash
	hash[31] = 1
	s.Equal(500, errorCode(s.ledger.Attribute(ctx, &domain.Organization{ID: 1}, &domain.Project{ID: 1}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 1}})))
}

func (s *ledgerSuite) TestRecountOrg_RebuildsFromHistory() {
	ctx := context.Background()
	org := snow.ID(1)
	project := snow.ID(1)

	rootID := s.seedTreeNode(nil)
	childID := s.seedTreeNode(&rootID)
	keepHash, keepID := s.seedChunk(10)
	nestedHash, nestedID := s.seedChunk(20)
	staleHash, staleID := s.seedChunk(999)
	s.seedFile(rootID, keepID)
	s.seedFile(childID, nestedID)
	s.seedCommit(project, rootID)
	s.seedLink(project, staleID, 999)

	s.NoError(s.ledger.RecountOrg(ctx, org))

	sizes, err := s.ledger.AttributedSizes(ctx, project, []domain.Hash{keepHash, nestedHash, staleHash})
	s.NoError(err)
	s.Equal(map[domain.Hash]int64{keepHash: 10, nestedHash: 20}, sizes)
	s.Equal(int64(30), s.projectUsed(project))
	s.Equal(int64(30), s.orgUsed(org))
}
