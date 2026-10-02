package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type TagRepositorySuite struct {
	baseSuite
}

func TestTagRepositorySuite(t *testing.T) {
	suite.Run(t, new(TagRepositorySuite))
}

func (s *TagRepositorySuite) TestCRUD() {
	ctx := context.Background()
	repo := NewTagRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)
	userID := seedPBACUser(s.T(), s.db, 42)

	created, err := repo.CreateTag(ctx, domain.Tag{
		ID:        1001,
		ProjectID: projectID,
		Name:      "v1.0.0",
		CommitID:  commitID,
		Message:   "first release",
		UserID:    userID,
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(1001), created.ID)
	s.Equal(projectID, created.ProjectID)
	s.Equal("v1.0.0", created.Name)
	s.Equal(commitID, created.CommitID)
	s.Equal("first release", created.Message)
	s.Equal(userID, created.UserID)
	s.False(created.CreatedAt.IsZero())

	got, err := repo.GetTagByName(ctx, projectID, "v1.0.0")
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)
	s.Equal(created.CommitID, got.CommitID)
	s.Equal(created.Message, got.Message)

	_, err = repo.GetTagByName(ctx, projectID, "missing")
	requireRecordNotFound(s.T(), err)

	_, err = repo.CreateTag(ctx, domain.Tag{
		ID:        1002,
		ProjectID: projectID,
		Name:      "v1.0.0",
		CommitID:  commitID,
		UserID:    userID,
	})
	s.Require().Error(err, "a duplicate tag name in the same project must be rejected")
	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)

	other, err := repo.CreateTag(ctx, domain.Tag{
		ID:        1003,
		ProjectID: otherProjectID,
		Name:      "v1.0.0",
		CommitID:  commitID,
		UserID:    userID,
	})
	s.Require().NoError(err, "the same tag name in another project must be allowed")
	s.Equal(otherProjectID, other.ProjectID)

	requireRecordNotFound(s.T(), repo.DeleteTag(ctx, otherProjectID, created.ID))
	_, err = repo.GetTagByName(ctx, projectID, "v1.0.0")
	s.Require().NoError(err, "a mismatched project delete must not remove the tag")

	s.Require().NoError(repo.DeleteTag(ctx, projectID, created.ID))
	_, err = repo.GetTagByName(ctx, projectID, "v1.0.0")
	requireRecordNotFound(s.T(), err)
	requireRecordNotFound(s.T(), repo.DeleteTag(ctx, projectID, created.ID))

	_, err = repo.GetTagByName(ctx, otherProjectID, "v1.0.0")
	s.Require().NoError(err, "deleting one project's tag must not touch another project's tag")
}

func (s *TagRepositorySuite) TestListTags() {
	ctx := context.Background()
	repo := NewTagRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)
	userID := seedPBACUser(s.T(), s.db, 42)

	list, err := repo.ListTags(ctx, projectID, 10, nil, 0)
	s.Require().NoError(err)
	s.Empty(list)

	now := time.Now().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		tag, err := repo.CreateTag(ctx, domain.Tag{
			ID:        snow.ID(2000 + i),
			ProjectID: projectID,
			Name:      fmt.Sprintf("v1.0.%d", i),
			CommitID:  commitID,
			UserID:    userID,
		})
		s.Require().NoError(err)
		ts := now.Add(time.Duration(i) * time.Minute)
		_, err = s.db.ExecContext(ctx, `UPDATE tags SET created_at = $1, updated_at = $2 WHERE id = $3`, ts, ts, tag.ID.Int64())
		s.Require().NoError(err)
	}

	_, err = repo.CreateTag(ctx, domain.Tag{
		ID:        3000,
		ProjectID: otherProjectID,
		Name:      "yanked",
		CommitID:  commitID,
		UserID:    userID,
	})
	s.Require().NoError(err)

	all, err := repo.ListTags(ctx, projectID, 10, nil, 0)
	s.Require().NoError(err)
	s.Len(all, 5, "tags must be isolated per project")
	s.Equal("v1.0.4", all[0].Name, "newest tag first")

	firstPage, err := repo.ListTags(ctx, projectID, 2, nil, 0)
	s.Require().NoError(err)
	s.Len(firstPage, 2)
	s.Equal("v1.0.4", firstPage[0].Name)
	s.Equal("v1.0.3", firstPage[1].Name)

	last := firstPage[len(firstPage)-1]
	secondPage, err := repo.ListTags(ctx, projectID, 2, &last.CreatedAt, last.ID)
	s.Require().NoError(err)
	s.Len(secondPage, 2)
	s.Equal("v1.0.2", secondPage[0].Name)
	s.Equal("v1.0.1", secondPage[1].Name)

	last = secondPage[len(secondPage)-1]
	thirdPage, err := repo.ListTags(ctx, projectID, 2, &last.CreatedAt, last.ID)
	s.Require().NoError(err)
	s.Len(thirdPage, 1)
	s.Equal("v1.0.0", thirdPage[0].Name)
}
