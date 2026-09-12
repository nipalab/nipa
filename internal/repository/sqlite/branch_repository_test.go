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
