package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type BYOStorageRepositorySuite struct {
	baseSuite
	repo *BYOStorageRepository
}

func TestBYOStorageRepositorySuite(t *testing.T) {
	suite.Run(t, new(BYOStorageRepositorySuite))
}

func (s *BYOStorageRepositorySuite) SetupTest() {
	s.baseSuite.SetupTest()
	s.repo = NewBYOStorageRepository(s.db)
}

func (s *BYOStorageRepositorySuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(), query, args...)
	s.Require().NoError(err)
}

func (s *BYOStorageRepositorySuite) server(id int64, name string) *domain.StorageServer {
	s.T().Helper()
	created, err := s.repo.CreateServer(context.Background(), domain.StorageServer{
		ID:           snow.ID(id),
		OwnerUserID:  1,
		Name:         name,
		Endpoint:     "https://s3.example.com",
		Region:       "auto",
		Bucket:       "bucket",
		Prefix:       "nipa",
		AccessKeyID:  "AKID",
		SecretSealed: []byte{1, 2, 3},
	})
	s.Require().NoError(err)
	return created
}

func (s *BYOStorageRepositorySuite) TestServerLifecycle() {
	ctx := context.Background()
	created := s.server(100, "primary")
	s.Equal(snow.ID(1), created.OwnerUserID)
	s.Equal([]byte{1, 2, 3}, created.SecretSealed)
	s.False(created.CreatedAt.IsZero())

	_, err := s.repo.CreateServer(ctx, domain.StorageServer{ID: 101, OwnerUserID: 1, Name: "primary", Endpoint: "e", Bucket: "b", AccessKeyID: "a", SecretSealed: []byte{0}})
	s.True(domain.IsErrorConflict(err))

	s.server(102, "another")
	count, err := s.repo.CountServers(ctx, 1)
	s.Require().NoError(err)
	s.EqualValues(2, count)

	list, err := s.repo.ListServers(ctx, 1)
	s.Require().NoError(err)
	s.Require().Len(list, 2)
	s.Equal("another", list[0].Name)

	created.Name = "renamed"
	created.Endpoint = "https://other.example.com"
	created.SecretSealed = []byte{9}
	updated, err := s.repo.UpdateServer(ctx, *created)
	s.Require().NoError(err)
	s.Equal("renamed", updated.Name)
	s.Equal("bucket", updated.Bucket)
	s.Equal([]byte{9}, updated.SecretSealed)
	s.False(updated.UpdatedAt.Before(created.UpdatedAt))

	got, err := s.repo.Server(ctx, 100)
	s.Require().NoError(err)
	s.Equal("https://other.example.com", got.Endpoint)

	_, err = s.repo.Server(ctx, 999)
	s.True(domain.IsErrorNotFound(err))
	_, err = s.repo.UpdateServer(ctx, domain.StorageServer{ID: 999, SecretSealed: []byte{0}})
	s.True(domain.IsErrorNotFound(err))

	s.Require().NoError(s.repo.DeleteServer(ctx, 102))
	s.True(domain.IsErrorNotFound(s.repo.DeleteServer(ctx, 102)))

	s.Require().NoError(dbtx.NewTransactor(s.db).WithinTx(ctx, func(ctx context.Context) error {
		return s.repo.LockOwner(ctx, 1)
	}))
}

func (s *BYOStorageRepositorySuite) TestAssignmentsAndInUse() {
	ctx := context.Background()
	s.server(200, "byo")

	inUse, err := s.repo.ServerInUse(ctx, 200)
	s.Require().NoError(err)
	s.False(inUse)

	def, err := s.repo.OrgDefault(ctx, 1)
	s.Require().NoError(err)
	s.Nil(def)

	s.Require().NoError(s.repo.SetOrgDefault(ctx, 1, 200, 1))
	def, err = s.repo.OrgDefault(ctx, 1)
	s.Require().NoError(err)
	s.Equal(snow.ID(200), *def)
	s.Require().NoError(s.repo.SetOrgDefault(ctx, 1, 200, 1))

	inUse, err = s.repo.ServerInUse(ctx, 200)
	s.Require().NoError(err)
	s.True(inUse)

	s.exec(`UPDATE organizations SET deleted = true WHERE id = 1`)
	inUse, err = s.repo.ServerInUse(ctx, 200)
	s.Require().NoError(err)
	s.False(inUse)
	s.exec(`UPDATE organizations SET deleted = false WHERE id = 1`)

	cleared, err := s.repo.ClearOrgDefault(ctx, 1)
	s.Require().NoError(err)
	s.True(cleared)
	cleared, err = s.repo.ClearOrgDefault(ctx, 1)
	s.Require().NoError(err)
	s.False(cleared)

	none, err := s.repo.ProjectServer(ctx, 1)
	s.Require().NoError(err)
	s.Nil(none)

	s.Require().NoError(s.repo.AssignProject(ctx, 1, 200))
	s.True(domain.IsErrorConflict(s.repo.AssignProject(ctx, 1, 200)))
	assigned, err := s.repo.ProjectServer(ctx, 1)
	s.Require().NoError(err)
	s.Equal(snow.ID(200), assigned.ID)
	s.Equal("byo", assigned.Name)

	inUse, err = s.repo.ServerInUse(ctx, 200)
	s.Require().NoError(err)
	s.True(inUse)
	s.True(domain.IsErrorConflict(s.repo.DeleteServer(ctx, 200)))
}

func (s *BYOStorageRepositorySuite) TestDeleteDropsDeletedOrgDefaults() {
	ctx := context.Background()
	s.server(300, "old")
	s.Require().NoError(s.repo.SetOrgDefault(ctx, 1, 300, 1))
	s.exec(`UPDATE organizations SET deleted = true WHERE id = 1`)
	s.Require().NoError(s.repo.DeleteServer(ctx, 300))
	def, err := s.repo.OrgDefault(ctx, 1)
	s.Require().NoError(err)
	s.Nil(def)
}

func (s *BYOStorageRepositorySuite) TestDeleteDropsDeletedProjectAssignments() {
	ctx := context.Background()
	s.server(400, "old")
	s.Require().NoError(s.repo.AssignProject(ctx, 1, 400))

	s.exec(`UPDATE organizations SET deleted = true WHERE id = 1`)
	inUse, err := s.repo.ServerInUse(ctx, 400)
	s.Require().NoError(err)
	s.False(inUse)
	s.exec(`UPDATE organizations SET deleted = false WHERE id = 1`)

	s.exec(`UPDATE projects SET deleted = true WHERE id = 1`)
	inUse, err = s.repo.ServerInUse(ctx, 400)
	s.Require().NoError(err)
	s.False(inUse)
	s.Require().NoError(s.repo.DeleteServer(ctx, 400))

	var rows int
	s.Require().NoError(s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_storage WHERE project_id = 1`).Scan(&rows))
	s.Zero(rows)
}
