package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type SSHKeyRepositorySuite struct {
	baseSuite
}

func TestSSHKeyRepositorySuite(t *testing.T) {
	suite.Run(t, new(SSHKeyRepositorySuite))
}

func (s *SSHKeyRepositorySuite) seedKey(userID snow.ID, id int64, fingerprint string) *domain.SSHPublicKey {
	s.T().Helper()
	key, err := NewSSHKeyRepository(s.db).Create(context.Background(), domain.SSHPublicKey{
		ID:          snow.ID(id),
		UserID:      userID,
		Name:        "key " + fingerprint,
		KeyType:     "ssh-ed25519",
		KeyBlob:     "blob-" + fingerprint,
		Fingerprint: fingerprint,
		Comment:     "test key",
	})
	s.Require().NoError(err)
	return key
}

func (s *SSHKeyRepositorySuite) TestCreateAndGet() {
	ctx := context.Background()
	repo := NewSSHKeyRepository(s.db)
	userID := seedPBACUser(s.T(), s.db, 1001)

	created, err := repo.Create(ctx, domain.SSHPublicKey{
		ID:          42,
		UserID:      userID,
		Name:        "work laptop",
		KeyType:     "ssh-ed25519",
		KeyBlob:     "AAAA",
		Fingerprint: "SHA256:abc",
		Comment:     "alice@laptop",
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(42), created.ID)
	s.Equal(userID, created.UserID)
	s.Equal("work laptop", created.Name)
	s.Equal("ssh-ed25519", created.KeyType)
	s.Equal("SHA256:abc", created.Fingerprint)
	s.Equal("alice@laptop", created.Comment)
	s.False(created.CreatedAt.IsZero())

	got, err := repo.GetByID(ctx, 42)
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)

	byFP, err := repo.GetByFingerprint(ctx, "SHA256:abc")
	s.Require().NoError(err)
	s.Equal(snow.ID(42), byFP.ID)
}

func (s *SSHKeyRepositorySuite) TestNotFound() {
	ctx := context.Background()
	repo := NewSSHKeyRepository(s.db)

	_, err := repo.GetByID(ctx, 99)
	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)

	_, err = repo.GetByFingerprint(ctx, "SHA256:missing")
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)
}

func (s *SSHKeyRepositorySuite) TestListAndCount() {
	ctx := context.Background()
	repo := NewSSHKeyRepository(s.db)
	alice := seedPBACUser(s.T(), s.db, 1001)
	bob := seedPBACUser(s.T(), s.db, 1002)

	s.seedKey(alice, 101, "SHA256:a1")
	s.seedKey(alice, 102, "SHA256:a2")
	s.seedKey(bob, 103, "SHA256:b1")

	keys, err := repo.ListByUser(ctx, alice)
	s.Require().NoError(err)
	s.Len(keys, 2)

	count, err := repo.CountByUser(ctx, alice)
	s.Require().NoError(err)
	s.Equal(2, count)

	count, err = repo.CountByUser(ctx, bob)
	s.Require().NoError(err)
	s.Equal(1, count)
}

func (s *SSHKeyRepositorySuite) TestDelete() {
	ctx := context.Background()
	repo := NewSSHKeyRepository(s.db)
	alice := seedPBACUser(s.T(), s.db, 1001)
	bob := seedPBACUser(s.T(), s.db, 1002)

	key := s.seedKey(alice, 101, "SHA256:a1")

	err := repo.Delete(ctx, bob, key.ID)
	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)

	s.Require().NoError(repo.Delete(ctx, alice, key.ID))
	_, err = repo.GetByID(ctx, key.ID)
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)
}

func (s *SSHKeyRepositorySuite) TestDuplicateFingerprint() {
	ctx := context.Background()
	repo := NewSSHKeyRepository(s.db)
	alice := seedPBACUser(s.T(), s.db, 1001)
	bob := seedPBACUser(s.T(), s.db, 1002)

	s.seedKey(alice, 101, "SHA256:shared")

	_, err := repo.Create(ctx, domain.SSHPublicKey{
		ID:          77,
		UserID:      bob,
		KeyType:     "ssh-ed25519",
		KeyBlob:     "AAAA",
		Fingerprint: "SHA256:shared",
	})
	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)
}
