package postgres

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func (s *ProjectRepositorySuite) TestDeletedAndRestore() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)
	node, err := snow.NewNode(1)
	s.Require().NoError(err)

	live, err := repo.Create(ctx, domain.Project{ID: node.Generate(), OrgID: 1, Name: "live"})
	s.Require().NoError(err)
	gone, err := repo.Create(ctx, domain.Project{ID: node.Generate(), OrgID: 1, Name: "gone"})
	s.Require().NoError(err)
	s.Require().NoError(repo.Delete(ctx, gone.ID))

	_, err = repo.GetDeleted(ctx, 1, live.ID)
	s.True(domain.IsErrorNotFound(err), "a live project is not a deleted one")
	deleted, err := repo.GetDeleted(ctx, 1, gone.ID)
	s.Require().NoError(err)
	s.True(deleted.Deleted)
	s.NotNil(deleted.DeletedAt)

	listed, err := repo.ListDeletedByOrgID(ctx, 1)
	s.Require().NoError(err)
	s.Require().Len(listed, 1)
	s.Equal(gone.ID, listed[0].ID)

	future := time.Now().Add(time.Hour)
	restored, err := repo.Restore(ctx, gone.ID, &future)
	s.Require().NoError(err)
	s.False(restored, "deleted before the window start")

	past := time.Now().Add(-time.Hour)
	restored, err = repo.Restore(ctx, gone.ID, &past)
	s.Require().NoError(err)
	s.True(restored)
	back, err := repo.Get(ctx, gone.ID)
	s.Require().NoError(err)
	s.False(back.Deleted)
	s.Nil(back.DeletedAt)

	restored, err = repo.Restore(ctx, gone.ID, nil)
	s.Require().NoError(err)
	s.False(restored, "a live project is not restored again")

	s.Require().NoError(repo.Delete(ctx, gone.ID))
	restored, err = repo.Restore(ctx, gone.ID, nil)
	s.Require().NoError(err)
	s.True(restored)
}
