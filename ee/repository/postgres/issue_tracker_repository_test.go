package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type IssueTrackerRepositorySuite struct {
	baseSuite
}

func TestIssueTrackerRepositorySuite(t *testing.T) {
	suite.Run(t, new(IssueTrackerRepositorySuite))
}

func (s *IssueTrackerRepositorySuite) tracker(id, orgID snow.ID, now time.Time) domain.IssueTracker {
	return domain.IssueTracker{
		ID: id, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://jira.example.com", AuthKind: "basic",
		Account: "bot@example.com", SecretSealed: []byte{1, 2, 3}, CreatedBy: 7, CreatedAt: now, UpdatedAt: now,
	}
}

func (s *IssueTrackerRepositorySuite) count(table string) int {
	var n int
	s.Require().NoError(s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func (s *IssueTrackerRepositorySuite) TestLifecycle() {
	ctx := context.Background()
	repo := NewIssueTrackerRepository(s.db)
	orgID := seedOrg(s.T(), s.q, "Studio", "studio")
	otherOrg := seedOrg(s.T(), s.q, "Other", "other")
	projectID := seedProject(s.T(), s.q, orgID.Int64(), "game")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var domErr *domain.Error

	_, err := repo.GetByOrg(ctx, orgID)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)

	tracker := s.tracker(10, orgID, now)
	created, err := repo.Create(ctx, tracker)
	s.Require().NoError(err)
	s.True(created.CreatedAt.Equal(now))
	created.CreatedAt, created.UpdatedAt = now, now
	s.Equal(tracker, *created)

	dup := tracker
	dup.ID = 11
	_, err = repo.Create(ctx, dup)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)

	tracker.Name = "Studio Jira"
	tracker.SecretSealed = []byte{4}
	tracker.UpdatedAt = now.Add(time.Minute)
	updated, err := repo.Update(ctx, tracker)
	s.Require().NoError(err)
	s.Equal("Studio Jira", updated.Name)
	s.Equal([]byte{4}, updated.SecretSealed)
	s.True(updated.UpdatedAt.Equal(now.Add(time.Minute)))

	wrongOrg := tracker
	wrongOrg.OrgID = otherOrg
	_, err = repo.Update(ctx, wrongOrg)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)

	got, err := repo.GetByOrg(ctx, orgID)
	s.Require().NoError(err)
	s.Equal(snow.ID(10), got.ID)

	settings := domain.ProjectIssueTracker{
		ProjectID: projectID, TrackerID: 10, Keys: []string{"ART", "GAME"}, LinkMergeRequests: true,
		LinkCommits: "all", MergedStatus: "Done", OpenedStatus: "In Review", IsActive: true, SetBy: 7, UpdatedAt: now,
	}
	_, err = repo.UpsertProject(ctx, settings)
	s.Require().NoError(err)
	settings.Keys = []string{"GAME"}
	settings.IsActive = false
	saved, err := repo.UpsertProject(ctx, settings)
	s.Require().NoError(err)
	s.Equal([]string{"GAME"}, saved.Keys)
	read, err := repo.GetProject(ctx, projectID)
	s.Require().NoError(err)
	s.False(read.IsActive)
	s.Equal("Done", read.MergedStatus)

	count, err := repo.CountProjects(ctx, 10)
	s.Require().NoError(err)
	s.Equal(int64(1), count)

	_, err = repo.Delete(ctx, orgID, 10)
	s.Error(err, "a used connection cannot be deleted")

	ok, err := repo.DeleteProject(ctx, projectID)
	s.Require().NoError(err)
	s.True(ok)
	ok, err = repo.DeleteProject(ctx, projectID)
	s.Require().NoError(err)
	s.False(ok)
	_, err = repo.GetProject(ctx, projectID)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)

	ok, err = repo.Delete(ctx, otherOrg, 10)
	s.Require().NoError(err)
	s.False(ok)
	ok, err = repo.Delete(ctx, orgID, 10)
	s.Require().NoError(err)
	s.True(ok)
}

func (s *IssueTrackerRepositorySuite) TestCleanup() {
	ctx := context.Background()
	repo := NewIssueTrackerRepository(s.db)
	now := time.Now().UTC()
	orgID := seedOrg(s.T(), s.q, "Studio", "studio")
	game := seedProject(s.T(), s.q, orgID.Int64(), "game")
	art := seedProject(s.T(), s.q, orgID.Int64(), "art")
	_, err := repo.Create(ctx, s.tracker(1, orgID, now))
	s.Require().NoError(err)
	for _, p := range []snow.ID{game, art} {
		_, err := repo.UpsertProject(ctx, domain.ProjectIssueTracker{ProjectID: p, TrackerID: 1, Keys: []string{"GAME"}, LinkCommits: "off", UpdatedAt: now})
		s.Require().NoError(err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO issue_links (id, project_id, tracker_id, issue_key, subject_kind, subject_id, relation, created_at)
		VALUES (1, $1, 1, 'GAME-1', 'commit', 5, 'mentions', $3), (2, $2, 1, 'GAME-2', 'commit', 6, 'closes', $3)`, game, art, now)
	s.Require().NoError(err)
	_, err = s.db.ExecContext(ctx, `INSERT INTO issue_tracker_jobs (id, tracker_id, project_id, issue_key, action, dedupe_key, created_at)
		VALUES (1, 1, $1, 'GAME-1', 'link', 'a', $3), (2, 1, $2, 'GAME-2', 'link', 'b', $3)`, game, art, now)
	s.Require().NoError(err)

	_, err = s.db.ExecContext(ctx, `UPDATE projects SET deleted = true WHERE id = $1`, game)
	s.Require().NoError(err)
	_, err = repo.GetProject(ctx, game)
	s.Error(err)
	s.Equal(1, s.count("issue_links"))
	s.Equal(1, s.count("issue_tracker_jobs"))

	_, err = s.db.ExecContext(ctx, `UPDATE organizations SET deleted = true WHERE id = $1`, orgID)
	s.Require().NoError(err)
	_, err = repo.GetByOrg(ctx, orgID)
	s.Error(err)
	s.Zero(s.count("project_issue_trackers"))
	s.Zero(s.count("issue_links"))
	s.Zero(s.count("issue_tracker_jobs"))
}

func (s *IssueTrackerRepositorySuite) TestLockOrg_SerializesPerOrg() {
	ctx := context.Background()
	repo := NewIssueTrackerRepository(s.db)
	transactor := dbtx.NewTransactor(s.db)

	tx, err := s.db.BeginTx(ctx, nil)
	s.Require().NoError(err)
	s.Require().NoError(repo.LockOrg(dbtx.WithTx(ctx, tx), 1))

	blocked, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	s.Error(transactor.WithinTx(blocked, func(ctx context.Context) error {
		return repo.LockOrg(ctx, 1)
	}), "a second writer waits for the org lock")
	s.NoError(transactor.WithinTx(ctx, func(ctx context.Context) error {
		return repo.LockOrg(ctx, 2)
	}), "other orgs are not blocked")

	s.Require().NoError(tx.Rollback())
}

func (s *IssueTrackerRepositorySuite) TestLinks() {
	ctx := context.Background()
	repo := NewIssueTrackerRepository(s.db)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	orgID := seedOrg(s.T(), s.q, "Studio", "studio")
	game := seedProject(s.T(), s.q, orgID.Int64(), "game")
	art := seedProject(s.T(), s.q, orgID.Int64(), "art")
	_, err := repo.Create(ctx, s.tracker(1, orgID, now))
	s.Require().NoError(err)
	got, err := repo.GetByID(ctx, 1)
	s.Require().NoError(err)
	s.Equal(orgID, got.OrgID)
	_, err = repo.GetByID(ctx, 99)
	s.Error(err)

	treeID := seedTreeNode(s.T(), s.db, "", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, game, treeID)
	branchID := seedBranch(s.T(), s.db, game, "GAME-1-rig", sql.NullInt64{})
	oldBranch := seedBranch(s.T(), s.db, game, "GAME-1-old", sql.NullInt64{})
	userID := seedUser(s.T(), s.q, "author", "author@example.com", sql.NullString{})
	_, err = s.db.ExecContext(ctx, `UPDATE branches SET deleted = true WHERE id = $1`, oldBranch)
	s.Require().NoError(err)
	_, err = s.db.ExecContext(ctx, `INSERT INTO merge_requests (id, number, project_id, source_branch_id, target_branch_id, source_branch_name, target_branch_name, title, status, created_by)
		VALUES (600, 3, $1, $2, $2, 'GAME-1-rig', 'main', 'New rig', 'open', $3)`, game, branchID, userID)
	s.Require().NoError(err)

	link := func(id int64, key, kind string, subject snow.ID, relation string) domain.IssueLink {
		return domain.IssueLink{ID: snow.ID(id), ProjectID: game, TrackerID: 1, IssueKey: key, SubjectKind: kind, SubjectID: subject, Relation: relation, CreatedAt: now}
	}
	for _, l := range []domain.IssueLink{
		link(1, "GAME-1", domain.IssueSubjectCommit, commitID, "closes"),
		link(2, "GAME-2", domain.IssueSubjectCommit, commitID, "mentions"),
		link(3, "GAME-1", domain.IssueSubjectMergeRequest, 600, "mentions"),
		link(4, "GAME-1", domain.IssueSubjectBranch, branchID, "mentions"),
		link(5, "GAME-1", domain.IssueSubjectBranch, oldBranch, "mentions"),
	} {
		s.Require().NoError(repo.UpsertIssueLink(ctx, l))
	}
	s.Require().NoError(repo.UpsertIssueLink(ctx, link(9, "GAME-1", domain.IssueSubjectMergeRequest, 600, "closes")))
	other := link(8, "GAME-1", domain.IssueSubjectCommit, commitID, "mentions")
	other.ProjectID = art
	s.Require().NoError(repo.UpsertIssueLink(ctx, other))

	mr, err := repo.ListIssueLinks(ctx, game, domain.IssueSubjectMergeRequest, 600)
	s.Require().NoError(err)
	s.Require().Len(mr, 1)
	s.Equal(snow.ID(3), mr[0].ID)
	s.Equal("closes", mr[0].Relation)

	batch, err := repo.ListIssueLinksBySubjects(ctx, game, domain.IssueSubjectCommit, []snow.ID{commitID, 12345})
	s.Require().NoError(err)
	s.Require().Len(batch, 2)
	s.Equal("GAME-1", batch[0].IssueKey)
	s.Equal("GAME-2", batch[1].IssueKey)

	page, err := repo.ListIssueSubjects(ctx, game, "GAME-1", 0, 2)
	s.Require().NoError(err)
	s.Require().Len(page, 2)
	s.True(page[0].Missing)
	s.Equal("GAME-1-rig", page[1].BranchName)
	rest, err := repo.ListIssueSubjects(ctx, game, "GAME-1", page[1].ID, 10)
	s.Require().NoError(err)
	s.Require().Len(rest, 2)
	s.Equal(int64(3), rest[0].MRNumber)
	s.Equal("New rig", rest[0].MRTitle)
	s.Equal("initial commit", rest[1].CommitMessage)
	s.False(rest[1].Missing)

	n, err := repo.DeleteIssueLinks(ctx, game, domain.IssueSubjectCommit, commitID, []string{"GAME-2", "GAME-9"})
	s.Require().NoError(err)
	s.Equal(int64(1), n)
	n, err = repo.DeleteIssueLinks(ctx, game, domain.IssueSubjectCommit, commitID, nil)
	s.Require().NoError(err)
	s.Zero(n)
	left, err := repo.ListIssueLinks(ctx, game, domain.IssueSubjectCommit, commitID)
	s.Require().NoError(err)
	s.Len(left, 1)
}
