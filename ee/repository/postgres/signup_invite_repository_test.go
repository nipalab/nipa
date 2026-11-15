package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
)

type SignupInviteRepositorySuite struct {
	baseSuite
}

func TestSignupInviteRepositorySuite(t *testing.T) {
	suite.Run(t, new(SignupInviteRepositorySuite))
}

func (s *SignupInviteRepositorySuite) TestLifecycle() {
	ctx := context.Background()
	node := newTestNode(s.T())
	admin, err := NewRegistrationRepository(s.db).CreateUnverifiedUser(ctx, domain.User{ID: node.Generate(), Name: "root", Email: "root@example.com"})
	s.Require().NoError(err)
	repo := NewSignupInviteRepository(s.db)
	now := time.Now().UTC().Truncate(time.Microsecond)

	created, err := repo.Upsert(ctx, domain.SignupInvite{ID: node.Generate(), Email: " Bob@Example.com ", InvitedBy: admin.ID})
	s.Require().NoError(err)
	s.Equal("bob@example.com", created.Email)
	s.Nil(created.ExpiresAt)

	allowed, err := repo.Allowed(ctx, "BOB@example.com", now)
	s.Require().NoError(err)
	s.True(allowed)
	allowed, err = repo.Allowed(ctx, "other@example.com", now)
	s.Require().NoError(err)
	s.False(allowed)

	expiry := now.Add(-time.Hour)
	again, err := repo.Upsert(ctx, domain.SignupInvite{ID: node.Generate(), Email: "bob@example.com", InvitedBy: admin.ID, ExpiresAt: &expiry})
	s.Require().NoError(err)
	s.Equal(created.ID, again.ID, "re-inviting keeps the row")
	s.Require().NotNil(again.ExpiresAt)
	allowed, err = repo.Allowed(ctx, "bob@example.com", now)
	s.Require().NoError(err)
	s.False(allowed, "an expired invite does not allow sign-up")

	list, err := repo.List(ctx)
	s.Require().NoError(err)
	s.Len(list, 1)

	deleted, err := repo.Delete(ctx, created.ID)
	s.Require().NoError(err)
	s.True(deleted)
	deleted, err = repo.Delete(ctx, created.ID)
	s.Require().NoError(err)
	s.False(deleted)
}
