package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type ServiceTokenRepositorySuite struct {
	baseSuite
}

func TestServiceTokenRepositorySuite(t *testing.T) {
	suite.Run(t, new(ServiceTokenRepositorySuite))
}

func (s *ServiceTokenRepositorySuite) TestLifecycle() {
	ctx := context.Background()
	repo := NewServiceTokenRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")
	userID := seedPBACUser(s.T(), s.db, 1001)
	expires := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	created, err := repo.Create(ctx, domain.ServiceToken{
		ID: 10, ProjectID: projectID, Name: "bugbase", TokenHash: "h1", Hint: "nipa_st_abcd",
		Scopes: []string{"read", "checks:write"}, CreatedBy: userID, ExpiresAt: &expires,
	})
	s.Require().NoError(err)
	s.Equal([]string{"read", "checks:write"}, created.Scopes)
	s.True(created.ExpiresAt.Equal(expires))
	s.Nil(created.RevokedAt)

	_, err = repo.Create(ctx, domain.ServiceToken{ID: 11, ProjectID: projectID, Name: "dup", TokenHash: "h1", Scopes: []string{"read"}, CreatedBy: userID})
	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)
	_, err = repo.Create(ctx, domain.ServiceToken{ID: 12, ProjectID: projectID, Name: "ci", TokenHash: "h2", CreatedBy: userID})
	s.Require().NoError(err)

	got, err := repo.Get(ctx, projectID, 10)
	s.Require().NoError(err)
	s.Equal("bugbase", got.Name)
	_, err = repo.Get(ctx, otherProjectID, 10)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)

	byHash, err := repo.GetByHash(ctx, "h2")
	s.Require().NoError(err)
	s.Equal(snow.ID(12), byHash.ID)
	s.Empty(byHash.Scopes)

	list, err := repo.ListByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Len(list, 2)
	s.Equal(snow.ID(12), list[0].ID)

	used := time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC)
	s.Require().NoError(repo.TouchLastUsed(ctx, 10, used))
	got, err = repo.Get(ctx, projectID, 10)
	s.Require().NoError(err)
	s.True(got.LastUsedAt.Equal(used))

	revoked, err := repo.Revoke(ctx, projectID, 10, used)
	s.Require().NoError(err)
	s.True(revoked.RevokedAt.Equal(used))
	_, err = repo.Revoke(ctx, projectID, 10, used)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)
}
