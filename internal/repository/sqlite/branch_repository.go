package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"gopkg.in/typ.v4/slices"
)

type BranchRepository struct {
	queries *sqlcSqlite.Queries
}

func NewBranchRepository(db *sql.DB) *BranchRepository {
	return &BranchRepository{queries: sqlcSqlite.New(db)}
}

func (b *BranchRepository) ListBranches(ctx context.Context, projectID snow.ID, limit int, updatedAfter *time.Time, lastID snow.ID) ([]*domain.Branch, error) {
	rows, err := b.queries.BranchList(ctx, sqlcSqlite.BranchListParams{
		ProjectID:     projectID.Int64(),
		Limit:         int64(limit),
		LastUpdatedAt: timePtrToNullTime(updatedAfter),
		LastID:        lastID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}

	return slices.Map(rows, func(b sqlcSqlite.Branch) *domain.Branch {
		return branchToDomain(b)
	}), nil
}

func (b *BranchRepository) GetDefaultBranch(ctx context.Context, projectID snow.ID) (*domain.Branch, error) {
	row, err := b.queries.BranchGetDefault(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return branchToDomain(row), nil
}

func (b *BranchRepository) GetByProjectIDAndID(ctx context.Context, projectID snow.ID, branchID snow.ID) (*domain.Branch, error) {
	row, err := b.queries.BranchGet(ctx, sqlcSqlite.BranchGetParams{
		ProjectID: projectID.Int64(),
		ID:        branchID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return branchToDomain(row), nil
}

func (b *BranchRepository) GetBranchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error) {
	row, err := b.queries.BranchGetByName(ctx, sqlcSqlite.BranchGetByNameParams{ProjectID: projectID.Int64(), Name: name})
	if err != nil {
		return nil, handleError(err)
	}
	return branchToDomain(row), nil
}

func (b *BranchRepository) GetCommit(ctx context.Context, commitID snow.ID) (*domain.Commit, error) {
	row, err := b.queries.CommitGet(ctx, commitID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return commitToDomain(row), nil
}

func (b *BranchRepository) GetTreeNode(ctx context.Context, id int64) (*domain.TreeNode, error) {
	row, err := b.queries.TreeNodeGet(ctx, id)
	if err != nil {
		return nil, handleError(err)
	}
	return treeNodeToDomain(row), nil
}

func (b *BranchRepository) GetTreeChildByName(ctx context.Context, parentID int64, name string) (*domain.TreeNode, error) {
	row, err := b.queries.TreeNodeGetChildByName(ctx, sqlcSqlite.TreeNodeGetChildByNameParams{
		ParentTreeID: sql.NullInt64{Int64: parentID, Valid: true},
		Name:         name,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return treeNodeToDomain(row), nil
}

func (b *BranchRepository) ListTreeChildren(ctx context.Context, parentID int64) ([]*domain.TreeNode, error) {
	rows, err := b.queries.TreeNodeListChildren(ctx, sql.NullInt64{Int64: parentID, Valid: true})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(t sqlcSqlite.TreeNode) *domain.TreeNode {
		return treeNodeToDomain(t)
	}), nil
}

func (b *BranchRepository) ListFilesByTree(ctx context.Context, treeID int64) ([]*domain.File, error) {
	rows, err := b.queries.FileListByTree(ctx, sql.NullInt64{Int64: treeID, Valid: true})
	if err != nil {
		return nil, handleError(err)
	}
	files := make([]*domain.File, 0, len(rows))
	for _, row := range rows {
		file := fileToDomain(row)
		chunks, err := b.queries.ChunkListByFile(ctx, row.ID)
		if err != nil {
			return nil, handleError(err)
		}
		file.Chunks = slices.Map(chunks, func(c sqlcSqlite.Chunk) domain.Chunk {
			return chunkToDomain(c)
		})
		files = append(files, file)
	}
	return files, nil
}

func branchToDomain(b sqlcSqlite.Branch) *domain.Branch {
	var commitID *snow.ID
	if b.CommitID.Valid {
		id := snow.ID(b.CommitID.Int64)
		commitID = &id
	}
	return &domain.Branch{
		ID:          snow.ID(b.ID),
		ProjectID:   snow.ID(b.ProjectID),
		Name:        b.Name,
		IsProtected: b.IsProtected,
		IsDefault:   b.IsDefault,
		CommitID:    commitID,
		UpdatedAt:   b.UpdatedAt,
		CreatedAt:   b.CreatedAt,
		Deleted:     b.Deleted,
		DeletedAt:   nullTimePtr(b.DeletedAt),
	}
}

func commitToDomain(c sqlcSqlite.Commit) *domain.Commit {
	var parent1ID, parent2ID *snow.ID
	if c.Parent1ID.Valid {
		id := snow.ID(c.Parent1ID.Int64)
		parent1ID = &id
	}
	if c.Parent2ID.Valid {
		id := snow.ID(c.Parent2ID.Int64)
		parent2ID = &id
	}
	return &domain.Commit{
		ID:        snow.ID(c.ID),
		Hash:      bytesToHash(c.Hash),
		ProjectID: snow.ID(c.ProjectID),
		TreeID:    c.TreeID,
		Parent1ID: parent1ID,
		Parent2ID: parent2ID,
		UserID:    snow.ID(c.UserID),
		Message:   c.Message,
		CreatedAt: c.CreatedAt,
	}
}

func treeNodeToDomain(t sqlcSqlite.TreeNode) *domain.TreeNode {
	return &domain.TreeNode{
		ID:        t.ID,
		Hash:      bytesToHash(t.Hash),
		Name:      t.Name,
		Mode:      int(t.Mode),
		ParentID:  nullInt64Ptr(t.ParentTreeID),
		CreatedAt: t.CreatedAt.Time,
	}
}

func fileToDomain(f sqlcSqlite.File) *domain.File {
	return &domain.File{
		ID:        f.ID,
		Hash:      bytesToHash(f.Hash),
		Name:      f.Name,
		Mode:      int(f.Mode),
		TreeID:    f.TreeID.Int64,
		SizeBytes: f.SizeBytes,
		IsBinary:  f.IsBinary,
		CreatedAt: f.CreatedAt.Time,
	}
}

func chunkToDomain(c sqlcSqlite.Chunk) domain.Chunk {
	return domain.Chunk{
		ID:        c.ID,
		Hash:      bytesToHash(c.Hash),
		SizeBytes: c.SizeBytes,
		CreatedAt: c.CreatedAt.Time,
	}
}

func bytesToHash(b []byte) domain.Hash {
	var h domain.Hash
	copy(h[:], b)
	return h
}
