package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type RegistrationRepositorySuite struct {
	baseSuite
}

func TestRegistrationRepositorySuite(t *testing.T) {
	suite.Run(t, new(RegistrationRepositorySuite))
}

func (s *RegistrationRepositorySuite) createUser() *domain.User {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	user, err := repo.CreateUnverifiedUser(ctx, domain.User{
		ID:       newTestNode(s.T()).Generate(),
		Name:     "alice",
		Email:    "alice@example.com",
		Password: "hash",
	})
	s.Require().NoError(err)
	return user
}

func (s *RegistrationRepositorySuite) TestCreateUnverifiedUser() {
	user := s.createUser()
	s.False(user.EmailVerified)
	s.Nil(user.EmailVerifiedAt)
	s.False(user.Deleted)
}

func (s *RegistrationRepositorySuite) TestEmailVerificationLifecycle() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	user := s.createUser()
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)

	s.Require().NoError(repo.UpsertEmailVerification(ctx, user.ID, "hash-1", expiresAt))
	gotID, gotExpiry, err := repo.EmailVerificationByToken(ctx, "hash-1")
	s.Require().NoError(err)
	s.Equal(user.ID, gotID)
	s.WithinDuration(expiresAt, gotExpiry, time.Second)

	s.Require().NoError(repo.UpsertEmailVerification(ctx, user.ID, "hash-2", expiresAt))
	_, _, err = repo.EmailVerificationByToken(ctx, "hash-1")
	requireRecordNotFound(s.T(), err)
	_, _, err = repo.EmailVerificationByToken(ctx, "hash-2")
	s.Require().NoError(err)

	s.Require().NoError(repo.SetEmailVerified(ctx, user.ID))
	verified, err := NewUserRepository(s.db).GetByID(ctx, user.ID)
	s.Require().NoError(err)
	s.True(verified.EmailVerified)
	s.NotNil(verified.EmailVerifiedAt)

	s.Require().NoError(repo.DeleteEmailVerification(ctx, user.ID))
	_, _, err = repo.EmailVerificationByToken(ctx, "hash-2")
	requireRecordNotFound(s.T(), err)
}

func (s *RegistrationRepositorySuite) TestDeleteExpiredEmailVerifications() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	user := s.createUser()

	s.Require().NoError(repo.UpsertEmailVerification(ctx, user.ID, "expired", time.Now().Add(-time.Minute)))
	s.Require().NoError(repo.DeleteExpiredEmailVerifications(ctx, time.Now()))
	_, _, err := repo.EmailVerificationByToken(ctx, "expired")
	requireRecordNotFound(s.T(), err)
}

func (s *RegistrationRepositorySuite) TestSeedOwnerQuota() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	user := s.createUser()

	s.Require().NoError(repo.SeedOwnerQuota(ctx, user.ID, 15*1024*1024*1024))
	quota, err := s.q.GetOwnerQuota(ctx, user.ID.Int64())
	s.Require().NoError(err)
	s.True(quota.Valid)
	s.Equal(int64(15*1024*1024*1024), quota.Int64)

	s.Require().NoError(repo.SeedOwnerQuota(ctx, user.ID, 1024))
	quota, err = s.q.GetOwnerQuota(ctx, user.ID.Int64())
	s.Require().NoError(err)
	s.True(quota.Valid)
	s.Equal(int64(1024), quota.Int64)
}

func (s *RegistrationRepositorySuite) TestIdentityLifecycle() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	user := s.createUser()

	node := newTestNode(s.T())
	identity := domain.UserIdentity{
		ID:                node.Generate(),
		UserID:            user.ID,
		Provider:          "github",
		ProviderAccountID: "1001",
		Email:             "alice@example.com",
	}
	s.Require().NoError(repo.CreateIdentity(ctx, identity))

	got, err := repo.IdentityByProviderAccount(ctx, "github", "1001")
	s.Require().NoError(err)
	s.Equal(identity.ID, got.ID)
	s.Equal(user.ID, got.UserID)
	s.Equal("alice@example.com", got.Email)

	byID, err := repo.IdentityByID(ctx, identity.ID)
	s.Require().NoError(err)
	s.Equal(identity.ID, byID.ID)

	count, err := repo.CountIdentities(ctx, user.ID)
	s.Require().NoError(err)
	s.Equal(1, count)

	listed, err := repo.ListIdentities(ctx, user.ID)
	s.Require().NoError(err)
	s.Len(listed, 1)
	s.Equal("github", listed[0].Provider)

	s.Require().Error(repo.DeleteIdentity(ctx, identity.ID, snow.ID(user.ID.Int64()+1)))
	s.Require().NoError(repo.DeleteIdentity(ctx, identity.ID, user.ID))
	count, err = repo.CountIdentities(ctx, user.ID)
	s.Require().NoError(err)
	s.Zero(count)

	s.Require().Error(repo.DeleteIdentity(ctx, identity.ID, user.ID), "deleting twice is not found")
}

func (s *RegistrationRepositorySuite) TestIdentityUniquePerProviderAccount() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	first := s.createUser()
	second, err := repo.CreateUnverifiedUser(ctx, domain.User{
		ID: newTestNode(s.T()).Generate(), Name: "bob", Email: "bob@example.com", Password: "hash",
	})
	s.Require().NoError(err)

	node := newTestNode(s.T())
	identity := domain.UserIdentity{ID: node.Generate(), UserID: first.ID, Provider: "github", ProviderAccountID: "1001"}
	s.Require().NoError(repo.CreateIdentity(ctx, identity))

	duplicate := identity
	duplicate.ID = newTestNode(s.T()).Generate()
	duplicate.UserID = second.ID
	err = repo.CreateIdentity(ctx, duplicate)
	s.Require().Error(err)
}

func (s *RegistrationRepositorySuite) TestOAuthLoginCodeSingleUse() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	user := s.createUser()

	expiresAt := time.Now().Add(2 * time.Minute)
	s.Require().NoError(repo.CreateOAuthLoginCode(ctx, "code-1", user.ID, expiresAt))

	userID, gotExpiry, err := repo.ConsumeOAuthLoginCode(ctx, "code-1")
	s.Require().NoError(err)
	s.Equal(user.ID, userID)
	s.WithinDuration(expiresAt, gotExpiry, time.Second)

	_, _, err = repo.ConsumeOAuthLoginCode(ctx, "code-1")
	requireRecordNotFound(s.T(), err)
}

func (s *RegistrationRepositorySuite) TestRegistrationWritesJoinTheTransaction() {
	ctx := context.Background()
	repo := NewRegistrationRepository(s.db)
	tx := dbtx.NewTransactor(s.db)
	node := newTestNode(s.T())

	err := tx.WithinTx(ctx, func(ctx context.Context) error {
		user, err := repo.CreateUnverifiedUser(ctx, domain.User{
			ID: node.Generate(), Name: "rollback", Email: "rollback@example.com", Password: "hash",
		})
		if err != nil {
			return err
		}
		return repo.UpsertEmailVerification(ctx, user.ID, "rollback-hash", time.Now().Add(time.Hour))
	})
	s.Require().NoError(err)

	_, _, err = repo.EmailVerificationByToken(ctx, "rollback-hash")
	s.Require().NoError(err)

	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := repo.CreateUnverifiedUser(ctx, domain.User{
			ID: node.Generate(), Name: "boom", Email: "boom@example.com", Password: "hash",
		}); err != nil {
			return err
		}
		return domain.NewErrorInternalServer("rollback")
	})
	s.Require().Error(err)

	_, err = NewUserRepository(s.db).GetByEmail(ctx, "boom@example.com")
	requireRecordNotFound(s.T(), err)
}
