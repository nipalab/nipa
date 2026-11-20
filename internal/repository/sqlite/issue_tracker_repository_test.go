package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

func TestIssueTrackerRepositorySQLite_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db, q := newMaintenanceTestDB(t)
	repo := NewIssueTrackerRepository(db)
	orgID := seedOrg(t, q, "Studio", "studio")
	otherOrg := seedOrg(t, q, "Other", "other")
	projectID := seedProject(t, q, orgID.Int64(), "game")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	require.NoError(t, repo.LockOrg(ctx, orgID))
	_, err := repo.GetByOrg(ctx, orgID)
	requireRecordNotFound(t, err)

	tracker := domain.IssueTracker{
		ID: 10, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://jira.example.com", AuthKind: "basic",
		Account: "bot@example.com", SecretSealed: []byte{1, 2, 3}, CreatedBy: 7, CreatedAt: now, UpdatedAt: now,
	}
	created, err := repo.Create(ctx, tracker)
	require.NoError(t, err)
	require.Equal(t, tracker, *created)

	dup := tracker
	dup.ID = 11
	_, err = repo.Create(ctx, dup)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)

	tracker.Name = "Studio Jira"
	tracker.SecretSealed = []byte{4}
	tracker.UpdatedAt = now.Add(time.Minute)
	updated, err := repo.Update(ctx, tracker)
	require.NoError(t, err)
	require.Equal(t, "Studio Jira", updated.Name)
	require.Equal(t, []byte{4}, updated.SecretSealed)
	require.True(t, updated.UpdatedAt.Equal(now.Add(time.Minute)))

	wrongOrg := tracker
	wrongOrg.OrgID = otherOrg
	_, err = repo.Update(ctx, wrongOrg)
	requireRecordNotFound(t, err)

	got, err := repo.GetByOrg(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, snow.ID(10), got.ID)

	count, err := repo.CountProjects(ctx, 10)
	require.NoError(t, err)
	require.Zero(t, count)

	_, err = repo.GetProject(ctx, projectID)
	requireRecordNotFound(t, err)
	settings := domain.ProjectIssueTracker{
		ProjectID: projectID, TrackerID: 10, Keys: []string{"ART", "GAME"}, LinkMergeRequests: true,
		LinkCommits: "all", CommentOnMerge: false, MergedStatus: "Done", OpenedStatus: "In Review",
		IsActive: true, SetBy: 7, UpdatedAt: now,
	}
	saved, err := repo.UpsertProject(ctx, settings)
	require.NoError(t, err)
	require.Equal(t, settings, *saved)

	settings.Keys = []string{"GAME"}
	settings.IsActive = false
	settings.LinkCommits = "off"
	saved, err = repo.UpsertProject(ctx, settings)
	require.NoError(t, err)
	require.Equal(t, []string{"GAME"}, saved.Keys)
	require.False(t, saved.IsActive)

	read, err := repo.GetProject(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, settings, *read)

	count, err = repo.CountProjects(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	settings.LinkCommits = "sometimes"
	_, err = repo.UpsertProject(ctx, settings)
	require.Error(t, err)

	ok, err := repo.DeleteProject(ctx, projectID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.DeleteProject(ctx, projectID)
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = repo.Delete(ctx, otherOrg, 10)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = repo.Delete(ctx, orgID, 10)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = repo.GetByOrg(ctx, orgID)
	requireRecordNotFound(t, err)
}

func TestIssueTrackerRepositorySQLite_Transaction(t *testing.T) {
	ctx := context.Background()
	db, q := newMaintenanceTestDB(t)
	repo := NewIssueTrackerRepository(db)
	orgID := seedOrg(t, q, "Studio", "studio")
	now := time.Now().UTC()

	err := dbtx.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		_, err := repo.Create(ctx, domain.IssueTracker{ID: 1, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://x", AuthKind: "bearer", SecretSealed: []byte{1}, CreatedAt: now, UpdatedAt: now})
		require.NoError(t, err)
		return domain.NewErrorConflict("rollback")
	})
	require.Error(t, err)
	_, err = repo.GetByOrg(ctx, orgID)
	requireRecordNotFound(t, err)
}

func TestIssueTrackerRepositorySQLite_Cleanup(t *testing.T) {
	ctx := context.Background()
	db, q := newMaintenanceTestDB(t)
	repo := NewIssueTrackerRepository(db)
	now := time.Now().UTC()
	orgID := seedOrg(t, q, "Studio", "studio")
	game := seedProject(t, q, orgID.Int64(), "game")
	art := seedProject(t, q, orgID.Int64(), "art")
	_, err := repo.Create(ctx, domain.IssueTracker{ID: 1, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://x", AuthKind: "bearer", SecretSealed: []byte{1}, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	for _, p := range []snow.ID{game, art} {
		_, err := repo.UpsertProject(ctx, domain.ProjectIssueTracker{ProjectID: p, TrackerID: 1, Keys: []string{"GAME"}, LinkCommits: "off", UpdatedAt: now})
		require.NoError(t, err)
	}
	maintenanceExec(t, db, `INSERT INTO issue_links (id, project_id, tracker_id, issue_key, subject_kind, subject_id, relation, created_at) VALUES (1, ?, 1, 'GAME-1', 'commit', 5, 'mentions', ?), (2, ?, 1, 'GAME-2', 'commit', 6, 'closes', ?)`, game, now, art, now)
	maintenanceExec(t, db, `INSERT INTO issue_tracker_jobs (id, tracker_id, project_id, issue_key, action, dedupe_key, created_at) VALUES (1, 1, ?, 'GAME-1', 'link', 'a', ?), (2, 1, ?, 'GAME-2', 'link', 'b', ?)`, game, now, art, now)

	maintenanceExec(t, db, `UPDATE projects SET deleted = 1 WHERE id = ?`, game)
	_, err = repo.GetProject(ctx, game)
	requireRecordNotFound(t, err)
	require.Equal(t, 1, countRows(t, db, "issue_links"))
	require.Equal(t, 1, countRows(t, db, "issue_tracker_jobs"))

	maintenanceExec(t, db, `UPDATE organizations SET deleted = 1 WHERE id = ?`, orgID)
	_, err = repo.GetByOrg(ctx, orgID)
	requireRecordNotFound(t, err)
	_, err = repo.GetProject(ctx, art)
	requireRecordNotFound(t, err)
	require.Zero(t, countRows(t, db, "issue_links"))
	require.Zero(t, countRows(t, db, "issue_tracker_jobs"))

	other := seedOrg(t, q, "Other", "other")
	proj := seedProject(t, q, other.Int64(), "tools")
	_, err = repo.Create(ctx, domain.IssueTracker{ID: 2, OrgID: other, Kind: "jira", Name: "Jira", BaseURL: "https://x", AuthKind: "bearer", SecretSealed: []byte{1}, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	_, err = repo.UpsertProject(ctx, domain.ProjectIssueTracker{ProjectID: proj, TrackerID: 2, Keys: []string{"TOOL"}, LinkCommits: "off", UpdatedAt: now})
	require.NoError(t, err)
	maintenanceExec(t, db, `DELETE FROM projects WHERE id = ?`, proj)
	_, err = repo.GetProject(ctx, proj)
	requireRecordNotFound(t, err)
	maintenanceExec(t, db, `DELETE FROM organizations WHERE id = ?`, other)
	_, err = repo.GetByOrg(ctx, other)
	requireRecordNotFound(t, err)
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func TestIssueTrackerRepositorySQLite_Links(t *testing.T) {
	ctx := context.Background()
	db, q := newMaintenanceTestDB(t)
	repo := NewIssueTrackerRepository(db)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	orgID := seedOrg(t, q, "Studio", "studio")
	game := seedProject(t, q, orgID.Int64(), "game")
	art := seedProject(t, q, orgID.Int64(), "art")
	_, err := repo.Create(ctx, domain.IssueTracker{ID: 1, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://x", AuthKind: "bearer", SecretSealed: []byte{1}, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, orgID, got.OrgID)
	_, err = repo.GetByID(ctx, 99)
	requireRecordNotFound(t, err)

	maintenanceExec(t, db, `INSERT INTO commits (id, hash, project_id, tree_id, user_id, message, created_at) VALUES (500, x'01', ?, 1, 1, 'Fix GAME-1', ?)`, game, now)
	maintenanceExec(t, db, `INSERT INTO merge_requests (id, number, project_id, source_branch_id, target_branch_id, source_branch_name, target_branch_name, title, status, created_by) VALUES (600, 3, ?, 1, 1, 'GAME-1-rig', 'main', 'New rig', 'open', 1)`, game)
	maintenanceExec(t, db, `INSERT INTO branches (id, project_id, name, key) VALUES (700, ?, 'GAME-1-rig', 'game-1-rig'), (701, ?, 'GAME-1-old', 'game-1-old')`, game, game)
	maintenanceExec(t, db, `UPDATE branches SET deleted = 1 WHERE id = 701`)

	link := func(id int64, key, kind string, subject int64, relation string) domain.IssueLink {
		return domain.IssueLink{ID: snow.ID(id), ProjectID: game, TrackerID: 1, IssueKey: key, SubjectKind: kind, SubjectID: snow.ID(subject), Relation: relation, CreatedAt: now}
	}
	for _, l := range []domain.IssueLink{
		link(1, "GAME-1", domain.IssueSubjectCommit, 500, "closes"),
		link(2, "GAME-2", domain.IssueSubjectCommit, 500, "mentions"),
		link(3, "GAME-1", domain.IssueSubjectMergeRequest, 600, "mentions"),
		link(4, "GAME-1", domain.IssueSubjectBranch, 700, "mentions"),
		link(5, "GAME-1", domain.IssueSubjectBranch, 701, "mentions"),
		link(6, "GAME-1", domain.IssueSubjectCommit, 501, "mentions"),
	} {
		require.NoError(t, repo.UpsertIssueLink(ctx, l))
	}
	require.NoError(t, repo.UpsertIssueLink(ctx, link(9, "GAME-1", domain.IssueSubjectMergeRequest, 600, "closes")))
	other := link(8, "GAME-1", domain.IssueSubjectCommit, 500, "mentions")
	other.ProjectID = art
	require.NoError(t, repo.UpsertIssueLink(ctx, other))
	require.Error(t, repo.UpsertIssueLink(ctx, link(10, "GAME-3", "tag", 1, "mentions")))

	mr, err := repo.ListIssueLinks(ctx, game, domain.IssueSubjectMergeRequest, 600)
	require.NoError(t, err)
	require.Len(t, mr, 1)
	require.Equal(t, snow.ID(3), mr[0].ID)
	require.Equal(t, "closes", mr[0].Relation)

	batch, err := repo.ListIssueLinksBySubjects(ctx, game, domain.IssueSubjectCommit, []snow.ID{500, 501, 502})
	require.NoError(t, err)
	require.Len(t, batch, 3)
	require.Equal(t, []string{"GAME-1", "GAME-2", "GAME-1"}, []string{batch[0].IssueKey, batch[1].IssueKey, batch[2].IssueKey})
	none, err := repo.ListIssueLinksBySubjects(ctx, game, domain.IssueSubjectCommit, nil)
	require.NoError(t, err)
	require.Empty(t, none)

	page, err := repo.ListIssueSubjects(ctx, game, "GAME-1", 0, 3)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.Equal(t, snow.ID(6), page[0].ID)
	require.True(t, page[0].Missing)
	require.True(t, page[1].Missing)
	require.Equal(t, "GAME-1-rig", page[2].BranchName)
	require.False(t, page[2].Missing)
	rest, err := repo.ListIssueSubjects(ctx, game, "GAME-1", page[2].ID, 10)
	require.NoError(t, err)
	require.Len(t, rest, 2)
	require.Equal(t, int64(3), rest[0].MRNumber)
	require.Equal(t, "New rig", rest[0].MRTitle)
	require.Equal(t, "open", rest[0].MRStatus)
	require.Equal(t, "Fix GAME-1", rest[1].CommitMessage)
	require.True(t, rest[1].CommitCreatedAt.Equal(now))

	n, err := repo.DeleteIssueLinks(ctx, game, domain.IssueSubjectCommit, 500, []string{"GAME-2", "GAME-9"})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	n, err = repo.DeleteIssueLinks(ctx, game, domain.IssueSubjectCommit, 500, nil)
	require.NoError(t, err)
	require.Zero(t, n)
	left, err := repo.ListIssueLinks(ctx, game, domain.IssueSubjectCommit, 500)
	require.NoError(t, err)
	require.Len(t, left, 1)
}
