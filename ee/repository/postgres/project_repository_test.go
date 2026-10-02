package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type ProjectRepositorySuite struct {
	baseSuite
}

func TestProjectRepositorySuite(t *testing.T) {
	suite.Run(t, new(ProjectRepositorySuite))
}

func (s *ProjectRepositorySuite) TestCRUD() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	snowNode, err := snow.NewNode(1)
	s.Require().NoError(err)
	created, err := repo.Create(ctx, domain.Project{
		ID:          snowNode.Generate(),
		OrgID:       1,
		Name:        "project-a",
		Description: "first project",
	})
	s.Require().NoError(err)
	s.NotZero(created.ID)
	s.Equal("project-a", created.Name)
	s.Equal("first project", created.Description)

	got, err := repo.Get(ctx, created.ID)
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)
	s.Equal(created.Name, got.Name)
	s.Equal(created.Description, got.Description)

	_, err = repo.Get(ctx, 999999)
	s.Require().Error(err)

	_, err = repo.Create(ctx, domain.Project{ID: snowNode.Generate(), OrgID: 1, Name: "project-b", Description: "second"})
	s.Require().NoError(err)

	projects, err := repo.ListByOrgID(ctx, 1)
	s.Require().NoError(err)
	s.Len(projects, 3)

	created2, err := repo.Create(ctx, domain.Project{ID: snowNode.Generate(), OrgID: 1, Name: "project-c", Description: "third"})
	s.Require().NoError(err)
	s.Require().NoError(repo.Delete(ctx, created2.ID))

	_, err = repo.Get(ctx, created2.ID)
	s.Require().Error(err)
}

func (s *ProjectRepositorySuite) TestCreateWithDefaultBranch_RollsBackProject() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	sharedBranchID := node.Generate()

	firstProjectID := node.Generate()
	_, err = repo.CreateWithDefaultBranch(ctx, domain.Project{
		ID: firstProjectID, OrgID: 1, Slug: "first", Name: "First",
	}, domain.Branch{ID: sharedBranchID, ProjectID: firstProjectID, Name: "main", IsDefault: true})
	s.Require().NoError(err)

	secondProjectID := node.Generate()
	_, err = repo.CreateWithDefaultBranch(ctx, domain.Project{
		ID: secondProjectID, OrgID: 1, Slug: "second", Name: "Second",
	}, domain.Branch{ID: sharedBranchID, ProjectID: secondProjectID, Name: "main", IsDefault: true})
	s.Require().Error(err)

	_, err = repo.Get(ctx, secondProjectID)
	s.Require().ErrorIs(err, sql.ErrNoRows, "a failed branch insert must roll back the project")
}

func (s *ProjectRepositorySuite) TestCreateWithDefaultBranch_DerivesSlug() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	projectID := node.Generate()

	created, err := repo.CreateWithDefaultBranch(ctx, domain.Project{
		ID: projectID, OrgID: 1, Name: "My Game",
	}, domain.Branch{ID: node.Generate(), ProjectID: projectID, Name: "main", IsDefault: true})
	s.Require().NoError(err)
	s.Equal("my-game", created.Slug)
}

func (s *ProjectRepositorySuite) TestCreateWithDefaultBranch_DuplicateSlug() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	firstID := node.Generate()
	_, err = repo.CreateWithDefaultBranch(ctx, domain.Project{
		ID: firstID, OrgID: 1, Slug: "game", Name: "Game",
	}, domain.Branch{ID: node.Generate(), ProjectID: firstID, Name: "main", IsDefault: true})
	s.Require().NoError(err)

	secondID := node.Generate()
	_, err = repo.CreateWithDefaultBranch(ctx, domain.Project{
		ID: secondID, OrgID: 1, Slug: "game", Name: "Game Again",
	}, domain.Branch{ID: node.Generate(), ProjectID: secondID, Name: "main", IsDefault: true})
	s.Require().Error(err)

	got, err := repo.GetByOrgIDAndSlug(ctx, 1, "game")
	s.Require().NoError(err)
	s.Equal(firstID, got.ID)
}

func (s *ProjectRepositorySuite) TestGetByOrgIDAndSlug_Success() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "project-a")

	got, err := repo.GetByOrgIDAndSlug(ctx, 1, "project-a")
	s.Require().NoError(err)
	s.Equal(projectID, got.ID)
	s.Equal(snow.ID(1), got.OrgID)
	s.Equal("project-a", got.Slug)
	s.Equal("project-a", got.Name)
}

func (s *ProjectRepositorySuite) TestGetByOrgIDAndSlug_NotFound() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	seedProject(s.T(), s.q, 1, "project-a")

	_, err := repo.GetByOrgIDAndSlug(ctx, 1, "does-not-exist")
	s.Require().ErrorIs(err, sql.ErrNoRows)
}

func (s *ProjectRepositorySuite) TestGetByOrgIDAndSlug_WrongOrg() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	seedProject(s.T(), s.q, 1, "project-a")

	_, err := repo.GetByOrgIDAndSlug(ctx, 2, "project-a")
	s.Require().ErrorIs(err, sql.ErrNoRows)
}

func (s *ProjectRepositorySuite) TestGetByOrgIDAndSlug_DeletedProject() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "project-a")
	s.Require().NoError(repo.Delete(ctx, projectID))

	_, err := repo.GetByOrgIDAndSlug(ctx, 1, "project-a")
	s.Require().ErrorIs(err, sql.ErrNoRows)
}

func (s *ProjectRepositorySuite) TestGetByOrgIDAndSlug_SameSlugDifferentOrg() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	_, err := s.db.ExecContext(ctx, `INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES (2, 'other', 'Other', 1)`)
	s.Require().NoError(err)

	node, err := snow.NewNode(1)
	s.Require().NoError(err)

	org1ID := node.Generate()
	org2ID := node.Generate()

	_, err = repo.Create(ctx, domain.Project{ID: org1ID, OrgID: 1, Slug: "shared", Name: "org1-project"})
	s.Require().NoError(err)
	_, err = repo.Create(ctx, domain.Project{ID: org2ID, OrgID: 2, Slug: "shared", Name: "org2-project"})
	s.Require().NoError(err)

	got1, err := repo.GetByOrgIDAndSlug(ctx, 1, "shared")
	s.Require().NoError(err)
	s.Equal(org1ID, got1.ID)

	got2, err := repo.GetByOrgIDAndSlug(ctx, 2, "shared")
	s.Require().NoError(err)
	s.Equal(org2ID, got2.ID)
}

func (s *ProjectRepositorySuite) TestUpdate() {
	ctx := context.Background()
	repo := NewProjectRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")

	updated, err := repo.Update(ctx, domain.Project{
		ID: projectID, Name: "Game 2", Description: "the sequel",
	})
	s.Require().NoError(err)
	s.Equal("Game 2", updated.Name)
	s.Equal("the sequel", updated.Description)
	s.Equal("game", updated.Slug, "slug is immutable")

	got, err := repo.Get(ctx, projectID)
	s.Require().NoError(err)
	s.Equal("Game 2", got.Name)
	s.Equal("the sequel", got.Description)
}
