package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

var testNodeCounter int64

func newTestNode(t *testing.T) snow.Node {
	t.Helper()
	id := atomic.AddInt64(&testNodeCounter, 1) % 256
	node, err := snow.NewNode(id)
	require.NoError(t, err)
	return node
}

func seedProject(t *testing.T, q *sqlcSqlite.Queries, orgID int64, name string) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := q.CreateProject(context.Background(), sqlcSqlite.CreateProjectParams{
		ID:          id.Int64(),
		OrgID:       orgID,
		Slug:        name,
		Name:        name,
		Description: name,
	})
	require.NoError(t, err)
	return id
}

func seedBranch(t *testing.T, db *sql.DB, projectID snow.ID, name string, commitID sql.NullInt64) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO branches (id, project_id, name, key, commit_id) VALUES (?, ?, ?, ?, ?)`,
		id.Int64(), projectID.Int64(), name, name, commitID,
	)
	require.NoError(t, err)
	return id
}

func TestBranchRepositorySQLite_GetByProjectIDAndID(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	branchID := seedBranch(t, db, projectID, "main", sql.NullInt64{})

	got, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	require.NoError(t, err)
	require.Equal(t, branchID, got.ID)
	require.Equal(t, projectID, got.ProjectID)
	require.Equal(t, "main", got.Name)
	require.False(t, got.IsProtected)
	require.False(t, got.IsDefault)
	require.Nil(t, got.CommitID)
	require.False(t, got.UpdatedAt.IsZero())
	require.False(t, got.CreatedAt.IsZero())
	require.False(t, got.Deleted)
	require.Nil(t, got.DeletedAt)
}

func TestBranchRepositorySQLite_GetByProjectIDAndID_WithCommitID(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	commitID := node.Generate()

	branchID := seedBranch(t, db, projectID, "feature", sql.NullInt64{Int64: commitID.Int64(), Valid: true})

	got, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	require.NoError(t, err)
	require.NotNil(t, got.CommitID)
	require.Equal(t, commitID, *got.CommitID)
}

func TestBranchRepositorySQLite_GetByProjectIDAndID_NotFound(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	_, err := repo.GetByProjectIDAndID(ctx, projectID, 999999)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetByProjectIDAndID_WrongProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")
	branchID := seedBranch(t, db, projectA, "main", sql.NullInt64{})

	_, err := repo.GetByProjectIDAndID(ctx, projectB, branchID)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_InsertTwoDefaultBranchesFails(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	firstID := node.Generate()
	_, err := db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES (?, ?, ?, ?, 1)`,
		firstID.Int64(), projectID.Int64(), "main", "main",
	)
	require.NoError(t, err)

	node = newTestNode(t)
	secondID := node.Generate()
	_, err = db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES (?, ?, ?, ?, 1)`,
		secondID.Int64(), projectID.Int64(), "develop", "develop",
	)
	require.Error(t, err)
}

func TestBranchRepositorySQLite_DefaultBranchPerProjectIsAllowed(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")

	for _, projectID := range []snow.ID{projectA, projectB} {
		node := newTestNode(t)
		id := node.Generate()
		_, err := db.ExecContext(ctx,
			`INSERT INTO branches (id, project_id, name, key, is_default) VALUES (?, ?, ?, ?, 1)`,
			id.Int64(), projectID.Int64(), "main", "main",
		)
		require.NoError(t, err)
	}
}

func TestBranchRepositorySQLite_ListBranches(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	seedBranch(t, db, projectID, "main", sql.NullInt64{})
	seedBranch(t, db, projectID, "develop", sql.NullInt64{})
	seedBranch(t, db, projectID, "feature", sql.NullInt64{})

	branches, err := repo.ListBranches(ctx, projectID, 10, nil, 0)
	require.NoError(t, err)
	require.Len(t, branches, 3)
}

func TestBranchRepositorySQLite_ListBranches_EmptyProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "empty-project")

	branches, err := repo.ListBranches(ctx, projectID, 10, nil, 0)
	require.NoError(t, err)
	require.Empty(t, branches)
}

func TestBranchRepositorySQLite_ListBranches_Limit(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	seedBranch(t, db, projectID, "branch-1", sql.NullInt64{})
	seedBranch(t, db, projectID, "branch-2", sql.NullInt64{})
	seedBranch(t, db, projectID, "branch-3", sql.NullInt64{})

	branches, err := repo.ListBranches(ctx, projectID, 2, nil, 0)
	require.NoError(t, err)
	require.Len(t, branches, 2)
}

func TestBranchRepositorySQLite_ListBranches_IsolatedPerProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")
	seedBranch(t, db, projectA, "main", sql.NullInt64{})
	seedBranch(t, db, projectA, "develop", sql.NullInt64{})
	seedBranch(t, db, projectB, "main", sql.NullInt64{})

	branchesA, err := repo.ListBranches(ctx, projectA, 10, nil, 0)
	require.NoError(t, err)
	require.Len(t, branchesA, 2)

	branchesB, err := repo.ListBranches(ctx, projectB, 10, nil, 0)
	require.NoError(t, err)
	require.Len(t, branchesB, 1)
	require.Equal(t, "main", branchesB[0].Name)
}

func TestBranchRepositorySQLite_ListBranches_Pagination(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	now := time.Now().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		node := newTestNode(t)
		id := node.Generate()
		ts := now.Add(time.Duration(i) * time.Minute)

		_, err := db.ExecContext(ctx,
			`INSERT INTO branches (id, project_id, name, key, updated_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id.Int64(), projectID.Int64(), "branch-"+string(rune('a'+i)), "branch-"+string(rune('a'+i)), ts, ts,
		)
		require.NoError(t, err)
	}

	firstPage, err := repo.ListBranches(ctx, projectID, 2, nil, 0)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)

	lastBranch := firstPage[len(firstPage)-1]
	secondPage, err := repo.ListBranches(ctx, projectID, 2, &lastBranch.UpdatedAt, lastBranch.ID)
	require.NoError(t, err)
	require.Len(t, secondPage, 2)

	for _, b := range secondPage {
		require.True(t, b.UpdatedAt.Before(lastBranch.UpdatedAt) ||
			(b.UpdatedAt.Equal(lastBranch.UpdatedAt) && b.ID < lastBranch.ID))
	}
}

func TestBranchRepositorySQLite_GetDefaultBranch(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	branchID := node.Generate()
	_, err := db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES (?, ?, ?, ?, 1)`,
		branchID.Int64(), projectID.Int64(), "main", "main",
	)
	require.NoError(t, err)

	got, err := repo.GetDefaultBranch(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, branchID, got.ID)
	require.Equal(t, projectID, got.ProjectID)
	require.Equal(t, "main", got.Name)
	require.True(t, got.IsDefault)
	require.False(t, got.IsProtected)
	require.Nil(t, got.CommitID)
	require.False(t, got.Deleted)
	require.Nil(t, got.DeletedAt)
}

func TestBranchRepositorySQLite_GetDefaultBranch_WithCommitID(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	branchID := node.Generate()
	commitID := node.Generate()

	_, err := db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default, commit_id) VALUES (?, ?, ?, ?, 1, ?)`,
		branchID.Int64(), projectID.Int64(), "main", "main", commitID.Int64(),
	)
	require.NoError(t, err)

	got, err := repo.GetDefaultBranch(ctx, projectID)
	require.NoError(t, err)
	require.NotNil(t, got.CommitID)
	require.Equal(t, commitID, *got.CommitID)
}

func TestBranchRepositorySQLite_GetDefaultBranch_NoBranches(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	_, err := repo.GetDefaultBranch(ctx, projectID)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetDefaultBranch_NoneDefault(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	seedBranch(t, db, projectID, "main", sql.NullInt64{})
	seedBranch(t, db, projectID, "develop", sql.NullInt64{})

	_, err := repo.GetDefaultBranch(ctx, projectID)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetDefaultBranch_IsolatedPerProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")

	node := newTestNode(t)
	branchID := node.Generate()
	_, err := db.ExecContext(ctx,
		`INSERT INTO branches (id, project_id, name, key, is_default) VALUES (?, ?, ?, ?, 1)`,
		branchID.Int64(), projectA.Int64(), "main", "main",
	)
	require.NoError(t, err)

	got, err := repo.GetDefaultBranch(ctx, projectA)
	require.NoError(t, err)
	require.Equal(t, branchID, got.ID)

	_, err = repo.GetDefaultBranch(ctx, projectB)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetBranchByName(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	branchID := seedBranch(t, db, projectID, "develop", sql.NullInt64{})

	got, err := repo.GetBranchByName(ctx, projectID, "develop")
	require.NoError(t, err)
	require.Equal(t, branchID, got.ID)
	require.Equal(t, projectID, got.ProjectID)
	require.Equal(t, "develop", got.Name)
	require.False(t, got.IsProtected)
	require.False(t, got.IsDefault)
	require.Nil(t, got.CommitID)
	require.False(t, got.Deleted)
	require.Nil(t, got.DeletedAt)
}

func TestBranchRepositorySQLite_GetBranchByName_WithCommitID(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	commitID := node.Generate()

	branchID := seedBranch(t, db, projectID, "develop", sql.NullInt64{Int64: commitID.Int64(), Valid: true})

	got, err := repo.GetBranchByName(ctx, projectID, "develop")
	require.NoError(t, err)
	require.Equal(t, branchID, got.ID)
	require.NotNil(t, got.CommitID)
	require.Equal(t, commitID, *got.CommitID)
}

func TestBranchRepositorySQLite_GetBranchByName_NotFound(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	seedBranch(t, db, projectID, "develop", sql.NullInt64{})

	_, err := repo.GetBranchByName(ctx, projectID, "missing")
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetBranchByName_IsolatedPerProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")
	branchAID := seedBranch(t, db, projectA, "main", sql.NullInt64{})
	branchBID := seedBranch(t, db, projectB, "main", sql.NullInt64{})

	got, err := repo.GetBranchByName(ctx, projectA, "main")
	require.NoError(t, err)
	require.Equal(t, branchAID, got.ID)

	gotB, err := repo.GetBranchByName(ctx, projectB, "main")
	require.NoError(t, err)
	require.Equal(t, branchBID, gotB.ID)
}

var testHashCounter int64

func testHashBytes() []byte {
	n := atomic.AddInt64(&testHashCounter, 1)
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b, uint64(n))
	return b
}

func seedTreeNode(t *testing.T, db *sql.DB, name string, parentID sql.NullInt64) int64 {
	t.Helper()

	res, err := db.ExecContext(context.Background(),
		`INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (?, ?, ?, ?)`,
		testHashBytes(), name, 0o755, parentID,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func seedFile(t *testing.T, db *sql.DB, treeID int64, name string, sizeBytes int64) int64 {
	t.Helper()

	res, err := db.ExecContext(context.Background(),
		`INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary) VALUES (?, ?, ?, ?, ?, ?)`,
		name, 0o644, treeID, testHashBytes(), sizeBytes, false,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func seedChunk(t *testing.T, db *sql.DB, fileID int64, index int) int64 {
	t.Helper()

	res, err := db.ExecContext(context.Background(),
		`INSERT INTO chunks (hash, size_bytes) VALUES (?, ?)`,
		testHashBytes(), 100,
	)
	require.NoError(t, err)
	chunkID, err := res.LastInsertId()
	require.NoError(t, err)

	_, err = db.ExecContext(context.Background(),
		`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (?, ?, ?)`,
		fileID, chunkID, index,
	)
	require.NoError(t, err)
	return chunkID
}

func seedCommit(t *testing.T, db *sql.DB, q *sqlcSqlite.Queries, projectID snow.ID, treeID int64) snow.ID {
	t.Helper()

	userID := seedUser(t, q, "committer", "committer@example.com", sql.NullString{})
	node := newTestNode(t)
	id := node.Generate()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
		id.Int64(), testHashBytes(), projectID.Int64(), treeID, userID.Int64(), "initial commit",
	)
	require.NoError(t, err)
	return id
}

func TestBranchRepositorySQLite_GetCommit(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	commitID := seedCommit(t, db, q, projectID, treeID)

	got, err := repo.GetCommit(ctx, commitID)
	require.NoError(t, err)
	require.Equal(t, commitID, got.ID)
	require.Equal(t, projectID, got.ProjectID)
	require.Equal(t, treeID, got.TreeID)
	require.Equal(t, "initial commit", got.Message)
	require.Nil(t, got.Parent1ID)
	require.Nil(t, got.Parent2ID)
}

func TestBranchRepositorySQLite_GetCommit_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	_, err := repo.GetCommit(ctx, 999999)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetTreeNode(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	rootID := seedTreeNode(t, db, "root", sql.NullInt64{})
	childID := seedTreeNode(t, db, "assets", sql.NullInt64{Int64: rootID, Valid: true})

	got, err := repo.GetTreeNode(ctx, childID)
	require.NoError(t, err)
	require.Equal(t, childID, got.ID)
	require.Equal(t, "assets", got.Name)
	require.NotNil(t, got.ParentID)
	require.Equal(t, rootID, *got.ParentID)
}

func TestBranchRepositorySQLite_GetTreeNode_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	_, err := repo.GetTreeNode(ctx, 999999)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_GetTreeChildByName(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	rootID := seedTreeNode(t, db, "root", sql.NullInt64{})
	childID := seedTreeNode(t, db, "assets", sql.NullInt64{Int64: rootID, Valid: true})

	got, err := repo.GetTreeChildByName(ctx, rootID, "assets")
	require.NoError(t, err)
	require.Equal(t, childID, got.ID)
	require.Equal(t, "assets", got.Name)
}

func TestBranchRepositorySQLite_GetTreeChildByName_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	rootID := seedTreeNode(t, db, "root", sql.NullInt64{})

	_, err := repo.GetTreeChildByName(ctx, rootID, "missing")
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_ListTreeChildren(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	rootID := seedTreeNode(t, db, "root", sql.NullInt64{})
	otherID := seedTreeNode(t, db, "other", sql.NullInt64{})
	seedTreeNode(t, db, "b-dir", sql.NullInt64{Int64: rootID, Valid: true})
	seedTreeNode(t, db, "a-dir", sql.NullInt64{Int64: rootID, Valid: true})
	seedTreeNode(t, db, "unrelated", sql.NullInt64{Int64: otherID, Valid: true})

	children, err := repo.ListTreeChildren(ctx, rootID)
	require.NoError(t, err)
	require.Len(t, children, 2)
	require.Equal(t, "a-dir", children[0].Name)
	require.Equal(t, "b-dir", children[1].Name)
}

func TestBranchRepositorySQLite_ListTreeChildren_Empty(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	rootID := seedTreeNode(t, db, "root", sql.NullInt64{})

	children, err := repo.ListTreeChildren(ctx, rootID)
	require.NoError(t, err)
	require.Empty(t, children)
}

func TestBranchRepositorySQLite_ListFilesByTree(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	fileID := seedFile(t, db, treeID, "a.txt", 10)
	chunkIdx1 := seedChunk(t, db, fileID, 1)
	chunkIdx0 := seedChunk(t, db, fileID, 0)
	seedFile(t, db, treeID, "b.txt", 20)

	files, err := repo.ListFilesByTree(ctx, treeID)
	require.NoError(t, err)
	require.Len(t, files, 2)

	require.Equal(t, "a.txt", files[0].Name)
	require.Equal(t, int64(10), files[0].SizeBytes)
	require.Len(t, files[0].Chunks, 2)
	require.Equal(t, chunkIdx0, files[0].Chunks[0].ID)
	require.Equal(t, chunkIdx1, files[0].Chunks[1].ID)

	require.Equal(t, "b.txt", files[1].Name)
	require.Empty(t, files[1].Chunks)
}

func TestBranchRepositorySQLite_ListFilesByTree_Empty(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})

	files, err := repo.ListFilesByTree(ctx, treeID)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestBranchRepositorySQLite_CreateBranch(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "create-project")
	node := newTestNode(t)
	branchID := node.Generate()
	commitID := node.Generate()

	created, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        branchID,
		ProjectID: projectID,
		Name:      "feature",
		CommitID:  &commitID,
	})
	require.NoError(t, err)
	require.Equal(t, branchID, created.ID)
	require.Equal(t, projectID, created.ProjectID)
	require.Equal(t, "feature", created.Name)
	require.False(t, created.IsProtected)
	require.False(t, created.IsDefault)
	require.NotNil(t, created.CommitID)
	require.Equal(t, commitID, *created.CommitID)
	require.False(t, created.UpdatedAt.IsZero())
	require.False(t, created.CreatedAt.IsZero())

	got, err := repo.GetBranchByName(ctx, projectID, "feature")
	require.NoError(t, err)
	require.Equal(t, branchID, got.ID)
	require.Equal(t, "feature", got.Name)
}

func TestBranchRepositorySQLite_CreateBranch_WithoutCommit(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "create-project")
	node := newTestNode(t)

	created, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectID,
		Name:      "empty-branch",
	})
	require.NoError(t, err)
	require.Nil(t, created.CommitID)
}

func TestBranchRepositorySQLite_CreateBranch_IsolatedPerProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")
	node := newTestNode(t)

	_, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectA,
		Name:      "feature",
	})
	require.NoError(t, err)

	_, err = repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectB,
		Name:      "feature",
	})
	require.NoError(t, err)
}

func TestBranchRepositorySQLite_CreateBranch_DuplicateName(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "create-project")
	node := newTestNode(t)

	_, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectID,
		Name:      "feature",
	})
	require.NoError(t, err)

	_, err = repo.CreateBranch(ctx, domain.Branch{
		ID:        node.Generate(),
		ProjectID: projectID,
		Name:      "feature",
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func TestBranchRepositorySQLite_GetCommitByHash(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	userID := seedUser(t, q, "committer", "committer@example.com", sql.NullString{})

	var hash domain.Hash
	hash[0] = 0xca
	hash[31] = 0xfe
	node := newTestNode(t)
	commitID := node.Generate()
	_, err := db.ExecContext(ctx,
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
		commitID.Int64(), hash.Bytes(), projectID.Int64(), treeID, userID.Int64(), "pinned commit",
	)
	require.NoError(t, err)

	got, err := repo.GetCommitByHash(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, commitID, got.ID)
	require.Equal(t, projectID, got.ProjectID)
	require.Equal(t, treeID, got.TreeID)
	require.Equal(t, "pinned commit", got.Message)
}

func TestBranchRepositorySQLite_GetCommitByHash_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	var missing domain.Hash
	missing[0] = 0xde
	_, err := repo.GetCommitByHash(ctx, missing)
	requireRecordNotFound(t, err)
}

func TestBranchRepositorySQLite_UpdateCommitIf(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	fromID := node.Generate()
	toID := node.Generate()
	treeID := newTestNode(t).Generate().Int64()
	_, err := db.ExecContext(ctx,
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
		fromID.Int64(), domain.Hash{1}.Bytes(), projectID.Int64(), treeID, 1, "from",
	)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
		toID.Int64(), domain.Hash{2}.Bytes(), projectID.Int64(), treeID, 1, "to",
	)
	require.NoError(t, err)

	branchID := seedBranch(t, db, projectID, "main", sql.NullInt64{Int64: fromID.Int64(), Valid: true})
	otherID := seedBranch(t, db, projectID, "feature", sql.NullInt64{Int64: fromID.Int64(), Valid: true})

	err = repo.UpdateCommitIf(ctx, branchID, &fromID, &toID)
	require.NoError(t, err)

	branch, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	require.NoError(t, err)
	require.NotNil(t, branch.CommitID)
	require.Equal(t, toID, *branch.CommitID)

	other, err := repo.GetByProjectIDAndID(ctx, projectID, otherID)
	require.NoError(t, err)
	require.Equal(t, fromID, *other.CommitID)
}

func TestBranchRepositorySQLite_UpdateCommitIf_CASMismatch(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	node := newTestNode(t)
	currentID := node.Generate()
	otherID := node.Generate()
	newHeadID := node.Generate()
	treeID := newTestNode(t).Generate().Int64()
	for _, c := range []struct {
		id   int64
		hash domain.Hash
		msg  string
	}{
		{currentID.Int64(), domain.Hash{1}, "current"},
		{otherID.Int64(), domain.Hash{2}, "other"},
		{newHeadID.Int64(), domain.Hash{3}, "new"},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
			c.id, c.hash.Bytes(), projectID.Int64(), treeID, 1, c.msg,
		)
		require.NoError(t, err)
	}

	branchID := seedBranch(t, db, projectID, "main", sql.NullInt64{Int64: newHeadID.Int64(), Valid: true})

	err := repo.UpdateCommitIf(ctx, branchID, &currentID, &otherID)
	require.True(t, domain.IsErrorConflict(err))

	branch, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	require.NoError(t, err)
	require.NotNil(t, branch.CommitID)
	require.Equal(t, newHeadID, *branch.CommitID)
}

func TestBranchRepositorySQLite_CommitLog_Chain(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	treeID := newTestNode(t).Generate().Int64()
	node := newTestNode(t)

	// build a chain: root <- mid <- tip
	rootID := node.Generate()
	midID := node.Generate()
	tipID := node.Generate()

	for _, c := range []struct {
		id       int64
		hash     domain.Hash
		parentID sql.NullInt64
		msg      string
	}{
		{rootID.Int64(), domain.Hash{1}, sql.NullInt64{}, "root commit"},
		{midID.Int64(), domain.Hash{2}, sql.NullInt64{Int64: rootID.Int64(), Valid: true}, "mid commit"},
		{tipID.Int64(), domain.Hash{3}, sql.NullInt64{Int64: midID.Int64(), Valid: true}, "tip commit"},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.id, c.hash.Bytes(), projectID.Int64(), treeID, c.parentID, 1, c.msg,
		)
		require.NoError(t, err)
	}

	got, err := repo.CommitLog(ctx, projectID, tipID, 10)
	require.NoError(t, err)
	require.Len(t, got, 3)

	// tip-first ordering
	require.Equal(t, tipID, got[0].ID)
	require.Equal(t, domain.Hash{3}, got[0].Hash)
	require.Equal(t, "tip commit", got[0].Message)
	require.NotNil(t, got[0].Parent1ID)
	require.Equal(t, midID, *got[0].Parent1ID)
	require.Nil(t, got[0].Parent2ID)

	require.Equal(t, midID, got[1].ID)
	require.Equal(t, "mid commit", got[1].Message)

	require.Equal(t, rootID, got[2].ID)
	require.Equal(t, "root commit", got[2].Message)
	require.Nil(t, got[2].Parent1ID)

	// author joined from users table (user id 1 = Super Admin)
	for _, e := range got {
		require.Equal(t, projectID, e.ProjectID)
		require.Equal(t, treeID, e.TreeID)
		require.Equal(t, int64(1), e.UserID.Int64())
		require.NotEmpty(t, e.AuthorName)
		require.NotEmpty(t, e.AuthorEmail)
		require.False(t, e.CreatedAt.IsZero())
	}
}

func TestBranchRepositorySQLite_CommitLog_Limit(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	treeID := newTestNode(t).Generate().Int64()
	node := newTestNode(t)

	rootID := node.Generate()
	midID := node.Generate()
	tipID := node.Generate()

	for _, c := range []struct {
		id       int64
		hash     domain.Hash
		parentID sql.NullInt64
		msg      string
	}{
		{rootID.Int64(), domain.Hash{1}, sql.NullInt64{}, "root"},
		{midID.Int64(), domain.Hash{2}, sql.NullInt64{Int64: rootID.Int64(), Valid: true}, "mid"},
		{tipID.Int64(), domain.Hash{3}, sql.NullInt64{Int64: midID.Int64(), Valid: true}, "tip"},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.id, c.hash.Bytes(), projectID.Int64(), treeID, c.parentID, 1, c.msg,
		)
		require.NoError(t, err)
	}

	got, err := repo.CommitLog(ctx, projectID, tipID, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, tipID, got[0].ID)
	require.Equal(t, midID, got[1].ID)
}

func TestBranchRepositorySQLite_CommitLog_ScopedToProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectA := seedProject(t, q, 1, "project-a")
	projectB := seedProject(t, q, 1, "project-b")
	treeID := newTestNode(t).Generate().Int64()
	node := newTestNode(t)

	rootID := node.Generate()
	tipID := node.Generate()

	for _, c := range []struct {
		projectID snow.ID
		id        int64
		hash      domain.Hash
		parentID  sql.NullInt64
	}{
		{projectA, rootID.Int64(), domain.Hash{1}, sql.NullInt64{}},
		{projectA, tipID.Int64(), domain.Hash{2}, sql.NullInt64{Int64: rootID.Int64(), Valid: true}},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.id, c.hash.Bytes(), c.projectID.Int64(), treeID, c.parentID, 1, "msg",
		)
		require.NoError(t, err)
	}

	// a commit belonging to projectB must not leak into projectA's log
	otherID := node.Generate()
	_, err := db.ExecContext(ctx,
		`INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		otherID.Int64(), domain.Hash{3}.Bytes(), projectB.Int64(), treeID, sql.NullInt64{}, 1, "other project",
	)
	require.NoError(t, err)

	got, err := repo.CommitLog(ctx, projectA, tipID, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, e := range got {
		require.Equal(t, projectA, e.ProjectID)
	}
}

func TestBranchRepositorySQLite_CommitLog_StartCommitUnrelatedToBranch(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")
	treeID := newTestNode(t).Generate().Int64()
	node := newTestNode(t)

	// two unrelated root commits; starting from the second should only return it
	firstID := node.Generate()
	secondID := node.Generate()
	for _, c := range []struct {
		id   int64
		hash domain.Hash
	}{
		{firstID.Int64(), domain.Hash{1}},
		{secondID.Int64(), domain.Hash{2}},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
			c.id, c.hash.Bytes(), projectID.Int64(), treeID, 1, "msg",
		)
		require.NoError(t, err)
	}

	got, err := repo.CommitLog(ctx, projectID, secondID, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, secondID, got[0].ID)
}

func TestBranchRepositorySQLite_CommitLog_DatabaseError(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "test-project")

	// drop the joined table to force a query error
	_, err := db.ExecContext(ctx, `DROP TABLE users`)
	require.NoError(t, err)

	_, err = repo.CommitLog(ctx, projectID, snow.ID(1), 10)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func TestBranchRepositorySQLite_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "game")
	branchID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})

	require.NoError(t, repo.RenameBranch(ctx, projectID, branchID, "renamed", "renamed"))
	renamed, err := repo.GetBranchByName(ctx, projectID, "renamed")
	require.NoError(t, err)
	require.Equal(t, "renamed", renamed.Name)

	require.NoError(t, repo.SetBranchProtection(ctx, projectID, branchID, true))
	protected, err := repo.GetByProjectIDAndID(ctx, projectID, branchID)
	require.NoError(t, err)
	require.True(t, protected.IsProtected)

	require.NoError(t, repo.SetDefaultBranch(ctx, projectID, branchID))
	def, err := repo.GetDefaultBranch(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, branchID, def.ID)

	require.NoError(t, repo.DeleteBranch(ctx, projectID, branchID))
	_, err = repo.GetBranchByName(ctx, projectID, "renamed")
	requireRecordNotFound(t, err)
	_, err = repo.GetByProjectIDAndID(ctx, projectID, branchID)
	requireRecordNotFound(t, err)
	_, err = repo.GetDefaultBranch(ctx, projectID)
	requireRecordNotFound(t, err)
	requireRecordNotFound(t, repo.DeleteBranch(ctx, projectID, branchID))

	branches, err := repo.ListBranches(ctx, projectID, 100, nil, 0)
	require.NoError(t, err)
	for _, branch := range branches {
		require.NotEqual(t, branchID, branch.ID)
	}
}

func TestBranchRepositorySQLite_CreateAfterDelete(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "game")
	oldID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	require.NoError(t, repo.DeleteBranch(ctx, projectID, oldID))

	recreated, err := repo.CreateBranch(ctx, domain.Branch{
		ID:        newTestNode(t).Generate(),
		ProjectID: projectID,
		Name:      "feature",
	})
	require.NoError(t, err, "a deleted branch name must be reusable")
	require.NotEqual(t, oldID, recreated.ID)
	require.Equal(t, "feature", recreated.Name)

	got, err := repo.GetBranchByName(ctx, projectID, "feature")
	require.NoError(t, err)
	require.Equal(t, recreated.ID, got.ID)
	_, err = repo.GetByProjectIDAndID(ctx, projectID, oldID)
	requireRecordNotFound(t, err)

	branches, err := repo.ListBranches(ctx, projectID, 100, nil, 0)
	require.NoError(t, err)
	count := 0
	for _, branch := range branches {
		if branch.Name == "feature" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestBranchRepositorySQLite_RenameToDeletedName(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "game")
	oldID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	require.NoError(t, repo.DeleteBranch(ctx, projectID, oldID))
	liveID := seedBranch(t, db, projectID, "trunk", sql.NullInt64{})

	require.NoError(t, repo.RenameBranch(ctx, projectID, liveID, "feature", "feature"))
	renamed, err := repo.GetBranchByName(ctx, projectID, "feature")
	require.NoError(t, err)
	require.Equal(t, liveID, renamed.ID)
}

func TestBranchRepositorySQLite_HasOpenMergeRequests(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)
	featureID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	mainID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	otherID := seedBranch(t, db, projectID, "other", sql.NullInt64{})

	insertMR := func(id int64, source, target snow.ID, status string) {
		_, err := db.ExecContext(ctx,
			`INSERT INTO merge_requests (id, project_id, source_branch_id, target_branch_id, source_branch_name, target_branch_name, title, status, created_by)
			 VALUES (?, ?, ?, ?, 'feature', 'main', 'mr', ?, ?)`,
			id, projectID.Int64(), source.Int64(), target.Int64(), status, userID.Int64(),
		)
		require.NoError(t, err)
	}

	open, err := repo.HasOpenMergeRequests(ctx, projectID, featureID)
	require.NoError(t, err)
	require.False(t, open)

	insertMR(1, featureID, mainID, domain.MergeRequestClosed)
	open, err = repo.HasOpenMergeRequests(ctx, projectID, featureID)
	require.NoError(t, err)
	require.False(t, open, "closed merge requests must not block deletion")

	insertMR(2, featureID, mainID, domain.MergeRequestOpen)
	open, err = repo.HasOpenMergeRequests(ctx, projectID, featureID)
	require.NoError(t, err)
	require.True(t, open, "an open merge request with the branch as source must block deletion")

	open, err = repo.HasOpenMergeRequests(ctx, projectID, mainID)
	require.NoError(t, err)
	require.True(t, open, "an open merge request with the branch as target must block deletion")

	open, err = repo.HasOpenMergeRequests(ctx, projectID, otherID)
	require.NoError(t, err)
	require.False(t, open, "unrelated branches must not be blocked")

	otherProject := seedProject(t, q, 1, "other-project")
	open, err = repo.HasOpenMergeRequests(ctx, otherProject, featureID)
	require.NoError(t, err)
	require.False(t, open, "merge requests must be scoped to the project")
}

func TestBranchRepositorySQLite_HasOpenMergeRequests_DatabaseError(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	projectID := seedProject(t, q, 1, "game")
	branchID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})

	_, err := db.ExecContext(ctx, `DROP TABLE merge_requests`)
	require.NoError(t, err)

	_, err = repo.HasOpenMergeRequests(ctx, projectID, branchID)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}
