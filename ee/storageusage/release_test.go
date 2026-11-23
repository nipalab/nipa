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

	keepHash := s.seedChunk(30)
	dropHashA := s.seedChunk(100)
	dropHashB := s.seedChunk(20)
	s.Require().NoError(s.confirm(ledger, ctx, org, keep, []usecase.LedgerChunk{{Hash: keepHash, SizeBytes: 30}}))
	s.Require().NoError(s.confirm(ledger, ctx, org, drop, []usecase.LedgerChunk{
		{Hash: dropHashA, SizeBytes: 100}, {Hash: dropHashB, SizeBytes: 20},
	}))
	s.Require().Equal(int64(150), s.orgUsed(org.ID))

	fresh := s.seedChunk(50)
	s.True(isQuotaExceeded(s.confirm(ledger, ctx, org, keep, []usecase.LedgerChunk{{Hash: fresh, SizeBytes: 50}})),
		"the quota is full before the release")

	s.softDeleteProject(drop.ID)
	released, err := ledger.ReleaseProject(ctx, drop.ID)
	s.Require().NoError(err)
	s.Equal(int64(120), released)
	s.Equal(int64(30), s.orgUsed(org.ID))
	s.Equal(int64(0), s.projectUsed(drop.ID))
	s.Equal(2, s.linkCount(drop.ID), "the rows stay until the purge; a restore charges them again")
	s.Equal(int64(30), s.projectUsed(keep.ID), "other projects keep their usage")

	s.NoError(s.confirm(ledger, ctx, org, keep, []usecase.LedgerChunk{{Hash: fresh, SizeBytes: 50}}),
		"the released bytes are usable again")
	s.Equal(int64(80), s.orgUsed(org.ID))

	again, err := ledger.ReleaseProject(ctx, drop.ID)
	s.Require().NoError(err)
	s.Zero(again, "a second release is a no-op")
	s.Equal(int64(80), s.orgUsed(org.ID))
}

// TestReleaseProject_SameContentStaysChargedToOtherProjects: the same bytes
// in two projects are two rows, each charged; releasing one leaves the other.
func (s *ledgerSuite) TestReleaseProject_SameContentStaysChargedToOtherProjects() {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	keep := &domain.Project{ID: 1}
	drop := &domain.Project{ID: s.seedProject(org.ID)}
	shared := s.seedChunk(40)
	chunk := []usecase.LedgerChunk{{Hash: shared, SizeBytes: 40}}
	s.Require().NoError(s.confirm(s.ledger, ctx, org, keep, chunk))
	s.Require().NoError(s.confirm(s.ledger, ctx, org, drop, chunk))
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
	hash := s.seedChunk(10)
	s.Require().NoError(s.confirm(s.ledger, ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}}))

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
	hash := s.seedChunk(10)
	s.softDeleteProject(project.ID)

	err := s.confirm(s.ledger, ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})
	s.True(domain.IsErrorNotFound(err), "got %v", err)
	s.Equal(0, s.linkCount(project.ID))
	s.Zero(s.orgUsed(org.ID))

	err = s.confirmProject(s.ledger, ctx, project.ID, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})
	s.True(domain.IsErrorNotFound(err), "merge output is rejected too, got %v", err)

	err = s.confirm(s.ledger, ctx, org, &domain.Project{ID: 987654}, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})
	s.True(domain.IsErrorNotFound(err), "unknown project, got %v", err)
}

func (s *ledgerSuite) TestRecountOrg_SkipsDeletedProjects() {
	ctx := context.Background()
	org := snow.ID(1)
	live := snow.ID(1)
	deleted := s.seedProject(org)

	s.seedRow(live, 10)
	s.seedRow(deleted, 70)
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_usage (project_id, used_bytes) VALUES ($1, 70)`, deleted.Int64())
	s.Require().NoError(err)
	s.softDeleteProject(deleted)

	s.NoError(s.ledger.RecountOrg(ctx, org))
	s.Equal(int64(10), s.orgUsed(org))
	s.Equal(int64(0), s.projectUsed(deleted))
	s.Equal(1, s.linkCount(deleted), "a deleted project's rows stay until the purge, uncharged")

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

	_, ledger = newErrorLedger(t, "org_billing_owner")
	_, err = ledger.ReleaseProject(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "owner lookup error")

	db, ledger := newErrorLedger(t, "org_usage")
	_, err = db.ExecContext(ctx, `INSERT INTO project_usage (project_id, used_bytes) VALUES (1, 5)`)
	require.NoError(t, err)
	_, err = ledger.ReleaseProject(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "org counter error")
}

func (s *ledgerSuite) restoreProjectRow(projectID snow.ID) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE projects SET deleted = false, deleted_at = NULL WHERE id = $1`, projectID.Int64())
	s.Require().NoError(err)
}

func (s *ledgerSuite) TestRestoreProject_ChargesTheRowsBack() {
	ctx := context.Background()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 1000})
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: s.seedProject(org.ID)}
	hashA := s.seedChunk(100)
	hashB := s.seedChunk(50)
	s.Require().NoError(s.confirm(ledger, ctx, org, project, []usecase.LedgerChunk{
		{Hash: hashA, SizeBytes: 100}, {Hash: hashB, SizeBytes: 50},
	}))

	s.softDeleteProject(project.ID)
	_, err := ledger.ReleaseProject(ctx, project.ID)
	s.Require().NoError(err)
	s.Zero(s.orgUsed(org.ID))

	s.restoreProjectRow(project.ID)
	charged, err := ledger.RestoreProject(ctx, project.ID)
	s.Require().NoError(err)
	s.Equal(int64(150), charged)
	s.Equal(int64(150), s.orgUsed(org.ID))
	s.Equal(int64(150), s.projectUsed(project.ID))
	s.Equal(2, s.linkCount(project.ID))

	again, err := ledger.RestoreProject(ctx, project.ID)
	s.Require().NoError(err)
	s.Equal(int64(150), again, "a repeated charge reports the same bytes")
	s.Equal(int64(150), s.orgUsed(org.ID), "and adds nothing")
}

func (s *ledgerSuite) TestRestoreProject_NeverReleasedIsNotDoubleCounted() {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: s.seedProject(org.ID)}
	hash := s.seedChunk(70)
	s.Require().NoError(s.confirm(s.ledger, ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 70}}))

	charged, err := s.ledger.RestoreProject(ctx, project.ID)
	s.Require().NoError(err)
	s.Equal(int64(70), charged)
	s.Equal(int64(70), s.orgUsed(org.ID))
}

func (s *ledgerSuite) TestRestoreProject_QuotaRejects() {
	ctx := context.Background()
	ledger := NewLedger(s.db, Config{DefaultQuotaBytes: 100})
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: s.seedProject(org.ID)}
	s.seedRow(project.ID, 80) // held but uncharged, like a released project
	fill := s.seedChunk(60)
	s.Require().NoError(s.confirm(ledger, ctx, org, &domain.Project{ID: 1}, []usecase.LedgerChunk{{Hash: fill, SizeBytes: 60}}))

	err := ledger.transactor.WithinTx(ctx, func(ctx context.Context) error {
		_, err := ledger.RestoreProject(ctx, project.ID)
		return err
	})
	s.True(isQuotaExceeded(err), "got %v", err)
	s.Equal(int64(60), s.orgUsed(org.ID))
	s.Zero(s.projectUsed(project.ID), "nothing is charged")
}

func (s *ledgerSuite) TestRestoreProject_ExternalAndDeletedAndUnknown() {
	ctx := context.Background()
	org := snow.ID(1)
	external := s.seedProject(org)
	s.seedRow(external, 40)
	_, err := s.db.ExecContext(ctx, `INSERT INTO storage_servers (id, owner_user_id, name, endpoint, bucket, access_key_id, secret_sealed)
		VALUES (78, 1, 'byo', 'https://s3.example.com', 'b', 'a', '\x00')`)
	s.Require().NoError(err)
	_, err = s.db.ExecContext(ctx, `INSERT INTO project_storage (project_id, storage_server_id) VALUES ($1, 78)`, external.Int64())
	s.Require().NoError(err)

	charged, err := s.ledger.RestoreProject(ctx, external)
	s.Require().NoError(err)
	s.Zero(charged, "own-storage projects are never charged")
	s.Zero(s.orgUsed(org))

	deleted := s.seedProject(org)
	s.softDeleteProject(deleted)
	_, err = s.ledger.RestoreProject(ctx, deleted)
	s.True(domain.IsErrorNotFound(err), "the project row must be restored first, got %v", err)

	_, err = s.ledger.RestoreProject(ctx, snow.ID(987654))
	s.True(domain.IsErrorNotFound(err), "got %v", err)
}

func TestLedger_RestoreProject_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	for _, table := range []string{"project_storage", "org_billing_owner", "project_usage", "org_usage"} {
		db, ledger := newErrorLedger(t, table)
		if table == "org_usage" {
			_, err := db.ExecContext(ctx, `INSERT INTO chunks (project_id, hash, size_bytes) VALUES (1, '\x01', 5)`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO tree_nodes (id, hash, name) VALUES (500, '\x02', 'root')`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO files (id, name, tree_id, hash, size_bytes) VALUES (500, 'f', 500, '\x03', 5)`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO file_chunks (file_id, chunk_id, chunk_index) SELECT 500, id, 0 FROM chunks`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (500, '\x04', 1, 500, 1, 'c')`)
			require.NoError(t, err)
		}
		_, err := ledger.RestoreProject(ctx, snow.ID(1))
		require.Equal(t, 500, errorCode(err), "%s missing", table)
	}
}
