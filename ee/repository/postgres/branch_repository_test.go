package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type BranchRepositorySuite struct {
	baseSuite
}

func TestBranchRepositorySuite(t *testing.T) {
	suite.Run(t, new(BranchRepositorySuite))
}

func (s *BranchRepositorySuite) TestGetByProjectIDAndID() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	branchID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})

	got, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	s.Require().NoError(err)
	s.Equal(branchID, got.ID)
	s.Equal(projectID, got.ProjectID)
	s.Equal("main", got.Name)
	s.False(got.IsProtected)
	s.False(got.IsDefault)
	s.Nil(got.CommitID)
	s.False(got.UpdatedAt.IsZero())
	s.False(got.CreatedAt.IsZero())
	s.False(got.Deleted)
	s.Nil(got.DeletedAt)
}

func (s *BranchRepositorySuite) TestGetByProjectIDAndID_WithCommitID() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)

	branchID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{Int64: commitID.Int64(), Valid: true})

	got, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	s.Require().NoError(err)
	s.Require().NotNil(got.CommitID)
	s.Equal(commitID, *got.CommitID)
}

func (s *BranchRepositorySuite) TestGetByProjectIDAndID_NotFound() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")

	_, err := repo.GetByProjectIDAndID(ctx, projectID, 999999)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetByProjectIDAndID_WrongProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")
	branchID := seedBranch(s.T(), s.db, projectA, "main", sql.NullInt64{})

	_, err := repo.GetByProjectIDAndID(ctx, projectB, branchID)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestInsertTwoDefaultBranchesFails() {
	ctx := context.Background()

	projectID := seedProject(s.T(), s.q, 1, "test-project")

	node := newTestNode(s.T())
	firstID := node.Generate()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES ($1, $2, $3, $4, TRUE)`,
		firstID.Int64(), projectID.Int64(), "main", "main",
	)
	s.Require().NoError(err)

	node = newTestNode(s.T())
	secondID := node.Generate()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES ($1, $2, $3, $4, TRUE)`,
		secondID.Int64(), projectID.Int64(), "develop", "develop",
	)
	s.Require().Error(err)
}

func (s *BranchRepositorySuite) TestDefaultBranchPerProjectIsAllowed() {
	ctx := context.Background()

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")

	for _, projectID := range []snow.ID{projectA, projectB} {
		node := newTestNode(s.T())
		id := node.Generate()
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO branches (id, project_id, name, key, is_default) VALUES ($1, $2, $3, $4, TRUE)`,
			id.Int64(), projectID.Int64(), "main", "main",
		)
		s.Require().NoError(err)
	}
}

func (s *BranchRepositorySuite) TestListBranches() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectID, "develop", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})

	branches, err := repo.ListBranches(ctx, projectID, 10, nil, 0)
	s.Require().NoError(err)
	s.Len(branches, 3)
}

func (s *BranchRepositorySuite) TestListBranches_EmptyProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "empty-project")

	branches, err := repo.ListBranches(ctx, projectID, 10, nil, 0)
	s.Require().NoError(err)
	s.Empty(branches)
}

func (s *BranchRepositorySuite) TestListBranches_Limit() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	seedBranch(s.T(), s.db, projectID, "branch-1", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectID, "branch-2", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectID, "branch-3", sql.NullInt64{})

	branches, err := repo.ListBranches(ctx, projectID, 2, nil, 0)
	s.Require().NoError(err)
	s.Len(branches, 2)
}

func (s *BranchRepositorySuite) TestListBranches_IsolatedPerProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")
	seedBranch(s.T(), s.db, projectA, "main", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectA, "develop", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectB, "main", sql.NullInt64{})

	branchesA, err := repo.ListBranches(ctx, projectA, 10, nil, 0)
	s.Require().NoError(err)
	s.Len(branchesA, 2)

	branchesB, err := repo.ListBranches(ctx, projectB, 10, nil, 0)
	s.Require().NoError(err)
	s.Len(branchesB, 1)
	s.Equal("main", branchesB[0].Name)
}

func (s *BranchRepositorySuite) TestListBranches_Pagination() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")

	now := time.Now().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		node := newTestNode(s.T())
		id := node.Generate()
		ts := now.Add(time.Duration(i) * time.Minute)
		name := "branch-" + string(rune('a'+i))

		_, err := s.db.ExecContext(ctx,
			`INSERT INTO branches (id, project_id, name, key, updated_at, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			id.Int64(), projectID.Int64(), name, name, ts, ts,
		)
		s.Require().NoError(err)
	}

	firstPage, err := repo.ListBranches(ctx, projectID, 2, nil, 0)
	s.Require().NoError(err)
	s.Len(firstPage, 2)

	lastBranch := firstPage[len(firstPage)-1]
	secondPage, err := repo.ListBranches(ctx, projectID, 2, &lastBranch.UpdatedAt, lastBranch.ID)
	s.Require().NoError(err)
	s.Len(secondPage, 2)

	for _, b := range secondPage {
		s.True(b.UpdatedAt.Before(lastBranch.UpdatedAt) ||
			(b.UpdatedAt.Equal(lastBranch.UpdatedAt) && b.ID < lastBranch.ID))
	}
}

func (s *BranchRepositorySuite) TestGetDefaultBranch() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")

	node := newTestNode(s.T())
	branchID := node.Generate()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES ($1, $2, $3, $4, TRUE)`,
		branchID.Int64(), projectID.Int64(), "main", "main",
	)
	s.Require().NoError(err)

	got, err := repo.GetDefaultBranch(ctx, projectID)
	s.Require().NoError(err)
	s.Equal(branchID, got.ID)
	s.Equal(projectID, got.ProjectID)
	s.Equal("main", got.Name)
	s.True(got.IsDefault)
	s.False(got.IsProtected)
	s.Nil(got.CommitID)
	s.False(got.Deleted)
	s.Nil(got.DeletedAt)
}

func (s *BranchRepositorySuite) TestGetDefaultBranch_WithCommitID() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)

	node := newTestNode(s.T())
	branchID := node.Generate()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default, commit_id) VALUES ($1, $2, $3, $4, TRUE, $5)`,
		branchID.Int64(), projectID.Int64(), "main", "main", commitID.Int64(),
	)
	s.Require().NoError(err)

	got, err := repo.GetDefaultBranch(ctx, projectID)
	s.Require().NoError(err)
	s.Require().NotNil(got.CommitID)
	s.Equal(commitID, *got.CommitID)
}

func (s *BranchRepositorySuite) TestGetDefaultBranch_NoBranches() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")

	_, err := repo.GetDefaultBranch(ctx, projectID)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetDefaultBranch_NoneDefault() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})
	seedBranch(s.T(), s.db, projectID, "develop", sql.NullInt64{})

	_, err := repo.GetDefaultBranch(ctx, projectID)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetDefaultBranch_IsolatedPerProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")

	node := newTestNode(s.T())
	branchID := node.Generate()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES ($1, $2, $3, $4, TRUE)`,
		branchID.Int64(), projectA.Int64(), "main", "main",
	)
	s.Require().NoError(err)

	got, err := repo.GetDefaultBranch(ctx, projectA)
	s.Require().NoError(err)
	s.Equal(branchID, got.ID)

	_, err = repo.GetDefaultBranch(ctx, projectB)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetBranchByName() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	branchID := seedBranch(s.T(), s.db, projectID, "develop", sql.NullInt64{})

	got, err := repo.GetBranchByName(ctx, projectID, "develop")
	s.Require().NoError(err)
	s.Equal(branchID, got.ID)
	s.Equal(projectID, got.ProjectID)
	s.Equal("develop", got.Name)
	s.False(got.IsProtected)
	s.False(got.IsDefault)
	s.Nil(got.CommitID)
	s.False(got.Deleted)
	s.Nil(got.DeletedAt)
}

func (s *BranchRepositorySuite) TestGetBranchByName_WithCommitID() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)

	branchID := seedBranch(s.T(), s.db, projectID, "develop", sql.NullInt64{Int64: commitID.Int64(), Valid: true})

	got, err := repo.GetBranchByName(ctx, projectID, "develop")
	s.Require().NoError(err)
	s.Equal(branchID, got.ID)
	s.Require().NotNil(got.CommitID)
	s.Equal(commitID, *got.CommitID)
}

func (s *BranchRepositorySuite) TestGetBranchByName_NotFound() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	seedBranch(s.T(), s.db, projectID, "develop", sql.NullInt64{})

	_, err := repo.GetBranchByName(ctx, projectID, "missing")
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetBranchByName_IsolatedPerProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")
	branchAID := seedBranch(s.T(), s.db, projectA, "main", sql.NullInt64{})
	branchBID := seedBranch(s.T(), s.db, projectB, "main", sql.NullInt64{})

	got, err := repo.GetBranchByName(ctx, projectA, "main")
	s.Require().NoError(err)
	s.Equal(branchAID, got.ID)

	gotB, err := repo.GetBranchByName(ctx, projectB, "main")
	s.Require().NoError(err)
	s.Equal(branchBID, gotB.ID)
}

func seedFile(t *testing.T, db *sql.DB, treeID int64, name string, sizeBytes int64) int64 {
	t.Helper()

	var id int64
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		name, 0o644, treeID, testHashBytes(), sizeBytes, false,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func seedChunk(t *testing.T, db *sql.DB, fileID int64, index int) int64 {
	t.Helper()

	var chunkID int64
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO chunks (hash, size_bytes) VALUES ($1, $2) RETURNING id`,
		testHashBytes(), 100,
	).Scan(&chunkID)
	require.NoError(t, err)

	_, err = db.ExecContext(context.Background(),
		`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES ($1, $2, $3)`,
		fileID, chunkID, index,
	)
	require.NoError(t, err)
	return chunkID
}

func (s *BranchRepositorySuite) TestGetCommit() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)

	got, err := repo.GetCommit(ctx, commitID)
	s.Require().NoError(err)
	s.Equal(commitID, got.ID)
	s.Equal(projectID, got.ProjectID)
	s.Equal(treeID, got.TreeID)
	s.Equal("initial commit", got.Message)
	s.Nil(got.Parent1ID)
	s.Nil(got.Parent2ID)
}

func (s *BranchRepositorySuite) TestGetCommit_NotFound() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	_, err := repo.GetCommit(ctx, 999999)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetTreeNode() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	rootID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	childID := seedTreeNode(s.T(), s.db, "assets", sql.NullInt64{Int64: rootID, Valid: true})

	got, err := repo.GetTreeNode(ctx, childID)
	s.Require().NoError(err)
	s.Equal(childID, got.ID)
	s.Equal("assets", got.Name)
	s.Require().NotNil(got.ParentID)
	s.Equal(rootID, *got.ParentID)
}

func (s *BranchRepositorySuite) TestGetTreeNode_NotFound() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	_, err := repo.GetTreeNode(ctx, 999999)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestGetTreeChildByName() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	rootID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	childID := seedTreeNode(s.T(), s.db, "assets", sql.NullInt64{Int64: rootID, Valid: true})

	got, err := repo.GetTreeChildByName(ctx, rootID, "assets")
	s.Require().NoError(err)
	s.Equal(childID, got.ID)
	s.Equal("assets", got.Name)
}

func (s *BranchRepositorySuite) TestGetTreeChildByName_NotFound() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	rootID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	_, err := repo.GetTreeChildByName(ctx, rootID, "missing")
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestListTreeChildren() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	rootID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	otherID := seedTreeNode(s.T(), s.db, "other", sql.NullInt64{})
	seedTreeNode(s.T(), s.db, "b-dir", sql.NullInt64{Int64: rootID, Valid: true})
	seedTreeNode(s.T(), s.db, "a-dir", sql.NullInt64{Int64: rootID, Valid: true})
	seedTreeNode(s.T(), s.db, "unrelated", sql.NullInt64{Int64: otherID, Valid: true})

	children, err := repo.ListTreeChildren(ctx, rootID)
	s.Require().NoError(err)
	s.Len(children, 2)
	s.Equal("a-dir", children[0].Name)
	s.Equal("b-dir", children[1].Name)
}

func (s *BranchRepositorySuite) TestListTreeChildren_Empty() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	rootID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	children, err := repo.ListTreeChildren(ctx, rootID)
	s.Require().NoError(err)
	s.Empty(children)
}

func (s *BranchRepositorySuite) TestListFilesByTree() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	fileID := seedFile(s.T(), s.db, treeID, "a.txt", 10)
	chunkIdx1 := seedChunk(s.T(), s.db, fileID, 1)
	chunkIdx0 := seedChunk(s.T(), s.db, fileID, 0)
	seedFile(s.T(), s.db, treeID, "b.txt", 20)

	files, err := repo.ListFilesByTree(ctx, treeID)
	s.Require().NoError(err)
	s.Len(files, 2)

	s.Equal("a.txt", files[0].Name)
	s.Equal(int64(10), files[0].SizeBytes)
	s.Len(files[0].Chunks, 2)
	s.Equal(chunkIdx0, files[0].Chunks[0].ID)
	s.Equal(chunkIdx1, files[0].Chunks[1].ID)

	s.Equal("b.txt", files[1].Name)
	s.Empty(files[1].Chunks)
}

func (s *BranchRepositorySuite) TestListFilesByTree_Empty() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	files, err := repo.ListFilesByTree(ctx, treeID)
	s.Require().NoError(err)
	s.Empty(files)
}

func (s *BranchRepositorySuite) TestCreateBranch() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "create-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	commitID := seedCommit(s.T(), s.db, s.q, projectID, treeID)
	node := newTestNode(s.T())
	branchID := node.Generate()

	created, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        branchID,
		ProjectID: projectID,
		Name:      "feature",
		CommitID:  &commitID,
	})
	s.Require().NoError(err)
	s.Equal(branchID, created.ID)
	s.Equal(projectID, created.ProjectID)
	s.Equal("feature", created.Name)
	s.False(created.IsProtected)
	s.False(created.IsDefault)
	s.Require().NotNil(created.CommitID)
	s.Equal(commitID, *created.CommitID)
	s.False(created.UpdatedAt.IsZero())
	s.False(created.CreatedAt.IsZero())

	got, err := repo.GetBranchByName(ctx, projectID, "feature")
	s.Require().NoError(err)
	s.Equal(branchID, got.ID)
	s.Equal("feature", got.Name)
}

func (s *BranchRepositorySuite) TestCreateBranch_WithoutCommit() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "create-project")
	node := newTestNode(s.T())

	created, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectID,
		Name:      "empty-branch",
	})
	s.Require().NoError(err)
	s.Nil(created.CommitID)
}

func (s *BranchRepositorySuite) TestCreateBranch_IsolatedPerProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")
	node := newTestNode(s.T())

	_, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectA,
		Name:      "feature",
	})
	s.Require().NoError(err)

	_, err = repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectB,
		Name:      "feature",
	})
	s.Require().NoError(err)
}

func (s *BranchRepositorySuite) TestCreateBranch_DuplicateName() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "create-project")
	node := newTestNode(s.T())

	_, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectID,
		Name:      "feature",
	})
	s.Require().NoError(err)

	_, err = repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectID,
		Name:      "feature",
	})
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)
}

func (s *BranchRepositorySuite) TestGetCommitByHash() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	userID := seedUser(s.T(), s.q, "committer", "committer@example.com", sql.NullString{})

	var hash domain.Hash
	hash[0] = 0xca
	hash[31] = 0xfe
	commitID := seedCommitRow(s.T(), s.db, projectID, treeID, userID.Int64(), hash, sql.NullInt64{}, "pinned commit")

	got, err := repo.GetCommitByHash(ctx, hash)
	s.Require().NoError(err)
	s.Equal(commitID, got.ID)
	s.Equal(projectID, got.ProjectID)
	s.Equal(treeID, got.TreeID)
	s.Equal("pinned commit", got.Message)
}

func (s *BranchRepositorySuite) TestGetCommitByHash_NotFound() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	var missing domain.Hash
	missing[0] = 0xde
	_, err := repo.GetCommitByHash(ctx, missing)
	requireRecordNotFound(s.T(), err)
}

func (s *BranchRepositorySuite) TestUpdateCommitIf() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	fromID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "from")
	toID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{2}, sql.NullInt64{}, "to")

	branchID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{Int64: fromID.Int64(), Valid: true})
	otherID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{Int64: fromID.Int64(), Valid: true})

	err := repo.UpdateCommitIf(ctx, branchID, &fromID, &toID)
	s.Require().NoError(err)

	branch, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	s.Require().NoError(err)
	s.Require().NotNil(branch.CommitID)
	s.Equal(toID, *branch.CommitID)

	other, err := repo.GetByProjectIDAndID(ctx, projectID, otherID)
	s.Require().NoError(err)
	s.Equal(fromID, *other.CommitID)
}

func (s *BranchRepositorySuite) TestUpdateCommitIf_CASMismatch() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	currentID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "current")
	otherID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{2}, sql.NullInt64{}, "other")
	newHeadID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{3}, sql.NullInt64{}, "new")

	branchID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{Int64: newHeadID.Int64(), Valid: true})

	err := repo.UpdateCommitIf(ctx, branchID, &currentID, &otherID)
	s.True(domain.IsErrorConflict(err))

	branch, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	s.Require().NoError(err)
	s.Require().NotNil(branch.CommitID)
	s.Equal(newHeadID, *branch.CommitID)
}

func (s *BranchRepositorySuite) TestCommitLog_Chain() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	rootID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "root commit")
	midID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{2}, sql.NullInt64{Int64: rootID.Int64(), Valid: true}, "mid commit")
	tipID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{3}, sql.NullInt64{Int64: midID.Int64(), Valid: true}, "tip commit")

	got, err := repo.CommitLog(ctx, projectID, tipID, 10)
	s.Require().NoError(err)
	s.Len(got, 3)

	s.Equal(tipID, got[0].ID)
	s.Equal(domain.Hash{3}, got[0].Hash)
	s.Equal("tip commit", got[0].Message)
	s.Require().NotNil(got[0].Parent1ID)
	s.Equal(midID, *got[0].Parent1ID)
	s.Nil(got[0].Parent2ID)

	s.Equal(midID, got[1].ID)
	s.Equal("mid commit", got[1].Message)

	s.Equal(rootID, got[2].ID)
	s.Equal("root commit", got[2].Message)
	s.Nil(got[2].Parent1ID)

	for _, e := range got {
		s.Equal(projectID, e.ProjectID)
		s.Equal(treeID, e.TreeID)
		s.Equal(int64(1), e.UserID.Int64())
		s.NotEmpty(e.AuthorName)
		s.NotEmpty(e.AuthorEmail)
		s.False(e.CreatedAt.IsZero())
	}
}

func (s *BranchRepositorySuite) TestCommitLogUntil_StopsAtBase() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	baseID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "base commit")
	firstID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{2}, sql.NullInt64{Int64: baseID.Int64(), Valid: true}, "first commit")
	secondID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{3}, sql.NullInt64{Int64: firstID.Int64(), Valid: true}, "second commit")

	got, err := repo.CommitLogUntil(ctx, projectID, secondID, baseID, 10)
	s.Require().NoError(err)
	s.Len(got, 2, "the merge base commit is excluded")
	s.Equal(secondID, got[0].ID)
	s.Equal(firstID, got[1].ID)
	s.Equal("second commit", got[0].Message)
	s.NotEmpty(got[0].AuthorName, "authors are joined from users")
	s.NotEmpty(got[0].AuthorEmail)

	all, err := repo.CommitLogUntil(ctx, projectID, secondID, 0, 10)
	s.Require().NoError(err)
	s.Len(all, 3, "a zero stop logs the whole chain")

	empty, err := repo.CommitLogUntil(ctx, projectID, baseID, baseID, 10)
	s.Require().NoError(err)
	s.Empty(empty)
}

func (s *BranchRepositorySuite) TestCommitLog_Limit() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	rootID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "root")
	midID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{2}, sql.NullInt64{Int64: rootID.Int64(), Valid: true}, "mid")
	tipID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{3}, sql.NullInt64{Int64: midID.Int64(), Valid: true}, "tip")

	got, err := repo.CommitLog(ctx, projectID, tipID, 2)
	s.Require().NoError(err)
	s.Len(got, 2)
	s.Equal(tipID, got[0].ID)
	s.Equal(midID, got[1].ID)
}

func (s *BranchRepositorySuite) TestCommitLog_ScopedToProject() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectA := seedProject(s.T(), s.q, 1, "project-a")
	projectB := seedProject(s.T(), s.q, 1, "project-b")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	rootID := seedCommitRow(s.T(), s.db, projectA, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "msg")
	tipID := seedCommitRow(s.T(), s.db, projectA, treeID, 1, domain.Hash{2}, sql.NullInt64{Int64: rootID.Int64(), Valid: true}, "msg")

	seedCommitRow(s.T(), s.db, projectB, treeID, 1, domain.Hash{3}, sql.NullInt64{}, "other project")

	got, err := repo.CommitLog(ctx, projectA, tipID, 10)
	s.Require().NoError(err)
	s.Len(got, 2)
	for _, e := range got {
		s.Equal(projectA, e.ProjectID)
	}
}

func (s *BranchRepositorySuite) TestCommitLog_StartCommitUnrelatedToBranch() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})

	seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{1}, sql.NullInt64{}, "msg")
	secondID := seedCommitRow(s.T(), s.db, projectID, treeID, 1, domain.Hash{2}, sql.NullInt64{}, "msg")

	got, err := repo.CommitLog(ctx, projectID, secondID, 10)
	s.Require().NoError(err)
	s.Len(got, 1)
	s.Equal(secondID, got[0].ID)
}

func (s *BranchRepositorySuite) TestCommitLog_DatabaseError() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "test-project")

	_, err := s.db.ExecContext(ctx, `DROP TABLE users CASCADE`)
	s.Require().NoError(err)

	_, err = repo.CommitLog(ctx, projectID, snow.ID(1), 10)
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(500, domErr.Code)
}

func (s *BranchRepositorySuite) TestLifecycle() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	branchID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})

	s.Require().NoError(repo.RenameBranch(ctx, projectID, branchID, "renamed", "renamed"))
	renamed, err := repo.GetBranchByName(ctx, projectID, "renamed")
	s.Require().NoError(err)
	s.Equal("renamed", renamed.Name)

	s.Require().NoError(repo.SetBranchProtection(ctx, projectID, branchID, true, 2))
	protected, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	s.Require().NoError(err)
	s.True(protected.IsProtected)
	s.EqualValues(2, protected.RequiredApprovals)

	s.Require().NoError(repo.SetDefaultBranch(ctx, projectID, branchID))
	def, err := repo.GetDefaultBranch(ctx, projectID)
	s.Require().NoError(err)
	s.Equal(branchID, def.ID)

	s.Require().NoError(repo.DeleteBranch(ctx, projectID, branchID))
	_, err = repo.GetBranchByName(ctx, projectID, "renamed")
	requireRecordNotFound(s.T(), err)
	_, err = repo.GetByProjectIDAndID(ctx, projectID, branchID)
	requireRecordNotFound(s.T(), err)
	_, err = repo.GetDefaultBranch(ctx, projectID)
	requireRecordNotFound(s.T(), err)
	requireRecordNotFound(s.T(), repo.DeleteBranch(ctx, projectID, branchID))

	branches, err := repo.ListBranches(ctx, projectID, 100, nil, 0)
	s.Require().NoError(err)
	for _, branch := range branches {
		s.NotEqual(branchID, branch.ID)
	}
}

func (s *BranchRepositorySuite) TestCreateAfterDelete() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	oldID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	s.Require().NoError(repo.DeleteBranch(ctx, projectID, oldID))

	recreated, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        newTestNode(s.T()).Generate(),
		ProjectID: projectID,
		Name:      "feature",
	})
	s.Require().NoError(err, "a deleted branch name must be reusable")
	s.NotEqual(oldID, recreated.ID)
	s.Equal("feature", recreated.Name)

	got, err := repo.GetBranchByName(ctx, projectID, "feature")
	s.Require().NoError(err)
	s.Equal(recreated.ID, got.ID)
	_, err = repo.GetByProjectIDAndID(ctx, projectID, oldID)
	requireRecordNotFound(s.T(), err)

	branches, err := repo.ListBranches(ctx, projectID, 100, nil, 0)
	s.Require().NoError(err)
	count := 0
	for _, branch := range branches {
		if branch.Name == "feature" {
			count++
		}
	}
	s.Equal(1, count)
}

func (s *BranchRepositorySuite) TestRenameToDeletedName() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	oldID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	s.Require().NoError(repo.DeleteBranch(ctx, projectID, oldID))
	liveID := seedBranch(s.T(), s.db, projectID, "trunk", sql.NullInt64{})

	s.Require().NoError(repo.RenameBranch(ctx, projectID, liveID, "feature", "feature"))
	renamed, err := repo.GetBranchByName(ctx, projectID, "feature")
	s.Require().NoError(err)
	s.Equal(liveID, renamed.ID)
}

func (s *BranchRepositorySuite) TestHasOpenMergeRequests() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)
	featureID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	mainID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})
	otherID := seedBranch(s.T(), s.db, projectID, "other", sql.NullInt64{})

	insertMR := func(id int64, source, target snow.ID, status string) {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO merge_requests (id, number, project_id, source_branch_id, target_branch_id, source_branch_name, target_branch_name, title, status, created_by)
			 VALUES ($1, $2, $3, $4, $5, 'feature', 'main', 'mr', $6, $7)`,
			id, id, projectID.Int64(), source.Int64(), target.Int64(), status, userID.Int64(),
		)
		s.Require().NoError(err)
	}

	open, err := repo.HasOpenMergeRequests(ctx, projectID, featureID)
	s.Require().NoError(err)
	s.False(open)

	insertMR(1, featureID, mainID, domain.MergeRequestClosed)
	open, err = repo.HasOpenMergeRequests(ctx, projectID, featureID)
	s.Require().NoError(err)
	s.False(open, "closed merge requests must not block deletion")

	insertMR(2, featureID, mainID, domain.MergeRequestOpen)
	open, err = repo.HasOpenMergeRequests(ctx, projectID, featureID)
	s.Require().NoError(err)
	s.True(open, "an open merge request with the branch as source must block deletion")

	open, err = repo.HasOpenMergeRequests(ctx, projectID, mainID)
	s.Require().NoError(err)
	s.True(open, "an open merge request with the branch as target must block deletion")

	open, err = repo.HasOpenMergeRequests(ctx, projectID, otherID)
	s.Require().NoError(err)
	s.False(open, "unrelated branches must not be blocked")

	otherProject := seedProject(s.T(), s.q, 1, "other-project")
	open, err = repo.HasOpenMergeRequests(ctx, otherProject, featureID)
	s.Require().NoError(err)
	s.False(open, "merge requests must be scoped to the project")
}

func (s *BranchRepositorySuite) TestHasOpenMergeRequests_DatabaseError() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	branchID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})

	_, err := s.db.ExecContext(ctx, `DROP TABLE merge_requests CASCADE`)
	s.Require().NoError(err)

	_, err = repo.HasOpenMergeRequests(ctx, projectID, branchID)
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(500, domErr.Code)
}
