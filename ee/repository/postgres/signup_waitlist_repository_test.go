package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type SignupWaitlistRepositorySuite struct {
	baseSuite
}

func TestSignupWaitlistRepositorySuite(t *testing.T) {
	suite.Run(t, new(SignupWaitlistRepositorySuite))
}

func (s *SignupWaitlistRepositorySuite) TestLifecycle() {
	ctx := context.Background()
	node := newTestNode(s.T())
	repo := NewSignupWaitlistRepository(s.db)

	var ids []snow.ID
	for _, email := range []string{" Ann@Example.com ", "bob@example.com", "carol@example.com"} {
		id := node.Generate()
		created, err := repo.Join(ctx, domain.WaitlistEntry{ID: id, Email: email, Name: "n", Source: "registration"})
		s.Require().NoError(err)
		s.True(created)
		ids = append(ids, id)
	}
	created, err := repo.Join(ctx, domain.WaitlistEntry{ID: node.Generate(), Email: "ann@example.com", Source: "oauth"})
	s.Require().NoError(err)
	s.False(created, "an address is listed once")

	all, err := repo.List(ctx, 0, nil, 10)
	s.Require().NoError(err)
	s.Require().Len(all, 3)
	s.Equal("ann@example.com", all[0].Email)
	s.Equal("registration", all[0].Source)
	page, err := repo.List(ctx, ids[0], nil, 1)
	s.Require().NoError(err)
	s.Require().Len(page, 1)
	s.Equal(ids[1], page[0].ID)

	_, err = NewRegistrationRepository(s.db).CreateUnverifiedUser(ctx, domain.User{ID: node.Generate(), Name: "Bob", Email: "Bob@example.com"})
	s.Require().NoError(err)
	pending, err := repo.Pending(ctx, 10)
	s.Require().NoError(err)
	s.Require().Len(pending, 2, "an address with an account is skipped")
	s.Equal(ids[0], pending[0].ID)
	s.Equal(ids[2], pending[1].ID)

	at := time.Now().UTC().Truncate(time.Microsecond)
	s.Require().NoError(repo.MarkNotified(ctx, []snow.ID{ids[0]}, at))
	pending, err = repo.Pending(ctx, 10)
	s.Require().NoError(err)
	s.Require().Len(pending, 1)
	s.Equal(ids[2], pending[0].ID)

	notified := false
	done, err := repo.List(ctx, 0, &notified, 10)
	s.Require().NoError(err)
	s.Require().Len(done, 1)
	s.Require().NotNil(done[0].NotifiedAt)
	s.True(done[0].NotifiedAt.Equal(at))
	waiting := true
	open, err := repo.List(ctx, 0, &waiting, 10)
	s.Require().NoError(err)
	s.Len(open, 2)

	deleted, err := repo.Delete(ctx, ids[2])
	s.Require().NoError(err)
	s.True(deleted)
	deleted, err = repo.Delete(ctx, ids[2])
	s.Require().NoError(err)
	s.False(deleted)
}
