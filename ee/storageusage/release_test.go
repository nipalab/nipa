package storageusage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (s *ledgerSuite) softDeleteProject(projectID snow.ID) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE projects SET deleted = true, deleted_at = now() WHERE id = $1`, projectID.Int64())
	s.Require().NoError(err)
}

func (s *ledgerSuite) TestReleaseProject_GivesBytesBack() {
	ctx := context.Background()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 150})
	org := &domain.Organization{ID: 1}
	keep := &domain.Project{ID: 1}
	drop := &domain.Project{ID: s.seedProject(org.ID)}

	keepHash, _ := s.seedChunk(30)
	dropHashA, _ := s.seedChunk(100)
	dropHashB, _ := s.seedChunk(20)
	s.Require().NoError(ledger.Attribute(ctx, org, keep, []usecase.LedgerChunk{{Hash: keepHash, SizeBytes: 30}}))
	s.Require().NoError(ledger.Attribute(ctx, org, drop, []usecase.LedgerChunk{
		{Hash: dropHashA, SizeBytes: 100}, {Hash: dropHashB, SizeBytes: 20},
	}))
	s.Require().Equal(int64(150), s.orgUsed(org.ID))

	fresh, _ := s.seedChunk(50)
	s.True(isQuotaExceeded(ledger.Attribute(ctx, org, keep, []usecase.LedgerChunk{{Hash: fresh, SizeBytes: 50}})),
		"the quota is full before the release")

	s.softDeleteProject(drop.ID)
	released, err := ledger.ReleaseProject(ctx, drop.ID)
	s.Require().NoError(err)
	s.Equal(int64(120), released)
	s.Equal(int64(30), s.orgUsed(org.ID))
	s.Equal(int64(0), s.projectUsed(drop.ID))
	s.Equal(0, s.linkCount(drop.ID))
	s.Equal(int64(30), s.projectUsed(keep.ID), "other projects keep their usage")

	s.NoError(ledger.Attribute(ctx, org, keep, []usecase.LedgerChunk{{Hash: fresh, SizeBytes: 50}}),
		"the released bytes are usable again")
	s.Equal(int64(80), s.orgUsed(org.ID))

	again, err := ledger.ReleaseProject(ctx, drop.ID)
	s.Require().NoError(err)
	s.Zero(again, "a second release is a no-op")
	s.Equal(int64(80), s.orgUsed(org.ID))
}

func (s *ledgerSuite) TestReleaseProject_SharedContentStaysChargedToOtherProjects() {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	keep := &domain.Project{ID: 1}
	drop := &domain.Project{ID: s.seedProject(org.ID)}
	shared, _ := s.seedChunk(40)
	chunk := []usecase.LedgerChunk{{Hash: shared, SizeBytes: 40}}
	s.Require().NoError(s.ledger.Attribute(ctx, org, keep, chunk))
	s.Require().NoError(s.ledger.Attribute(ctx, org, drop, chunk))
	s.Require().Equal(int64(80), s.orgUsed(org.ID))

	released, err := s.ledger.ReleaseProject(ctx, drop.ID)
	s.Require().NoError(err)
	s.Equal(int64(40), released)
	s.Equal(int64(40), s.orgUsed(org.ID))
	s.Equal(1, s.linkCount(keep.ID))
}

func (s *ledgerSuite) TestReleaseProject_EmptyProject() {
	project := s.seedProject(snow.ID(1))
	released, err := s.ledger.ReleaseProject(context.Background(), project)
	s.Require().NoError(err)
	s.Zero(released)
	s.Zero(s.orgUsed(snow.ID(1)))
}

func (s *ledgerSuite) TestReleaseProject_UnknownProject() {
	_, err := s.ledger.ReleaseProject(context.Background(), snow.ID(987654))
	s.True(domain.IsErrorNotFound(err), "got %v", err)
}

func (s *ledgerSuite) TestReleaseProject_JoinsTheCallersTransaction() {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: 1}
	hash, _ := s.seedChunk(10)
	s.Require().NoError(s.ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}}))

	errRollback := domain.NewErrorUser("rollback")
	err := s.ledger.transactor.WithinTx(ctx, func(ctx context.Context) error {
		released, err := s.ledger.ReleaseProject(ctx, project.ID)
		s.Require().NoError(err)
		s.Equal(int64(10), released)
		return errRollback
	})
	s.ErrorIs(err, errRollback)
	s.Equal(int64(10), s.orgUsed(org.ID), "a rolled back delete keeps the usage")
	s.Equal(1, s.linkCount(project.ID))
}

func (s *ledgerSuite) TestAttribute_RejectsDeletedProject() {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: s.seedProject(org.ID)}
	hash, _ := s.seedChunk(10)
	s.softDeleteProject(project.ID)

	err := s.ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})
	s.True(domain.IsErrorNotFound(err), "got %v", err)
	s.Equal(0, s.linkCount(project.ID))
	s.Zero(s.orgUsed(org.ID))

	err = s.ledger.AttributeProjectChunks(ctx, project.ID, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})
	s.True(domain.IsErrorNotFound(err), "merge output is rejected too, got %v", err)

	err = s.ledger.Attribute(ctx, org, &domain.Project{ID: 987654}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})
	s.True(domain.IsErrorNotFound(err), "unknown project, got %v", err)
}

func (s *ledgerSuite) TestRecountOrg_SkipsDeletedProjects() {
	ctx := context.Background()
	org := snow.ID(1)
	live := snow.ID(1)
	deleted := s.seedProject(org)

	liveRoot := s.seedTreeNode(nil)
	_, liveChunk := s.seedChunk(10)
	s.seedFile(liveRoot, liveChunk)
	s.seedCommit(live, liveRoot)

	deletedRoot := s.seedTreeNode(nil)
	_, deletedChunk := s.seedChunk(70)
	s.seedFile(deletedRoot, deletedChunk)
	s.seedCommit(deleted, deletedRoot)
	s.seedLink(deleted, deletedChunk, 70)
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_usage (project_id, used_bytes) VALUES ($1, 70)`, deleted.Int64())
	s.Require().NoError(err)
	s.softDeleteProject(deleted)

	s.NoError(s.ledger.RecountOrg(ctx, org))
	s.Equal(int64(10), s.orgUsed(org))
	s.Equal(int64(0), s.projectUsed(deleted))
	s.Equal(0, s.linkCount(deleted))

	summary, err := s.ledger.OrgStorage(ctx, org)
	s.Require().NoError(err)
	for _, project := range summary.Projects {
		if project.ProjectID == deleted {
			s.True(project.Deleted)
			s.Zero(project.UsedBytes)
		}
	}
}

func TestLedger_ReleaseProject_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	_, ledger := newErrorLedger(t, "project_usage")
	_, err := ledger.ReleaseProject(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "usage lookup error")

	_, ledger = newErrorLedger(t, "project_chunks")
	_, err = ledger.ReleaseProject(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "link delete error")

	_, ledger = newErrorLedger(t, "org_billing_owner")
	_, err = ledger.ReleaseProject(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "owner lookup error")

	db, ledger := newErrorLedger(t, "org_usage")
	_, err = db.ExecContext(ctx, `INSERT INTO project_usage (project_id, used_bytes) VALUES (1, 5)`)
	require.NoError(t, err)
	_, err = ledger.ReleaseProject(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "org counter error")
}
