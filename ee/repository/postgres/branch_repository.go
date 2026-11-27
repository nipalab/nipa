package postgres

import (
	"context"
	"database/sql"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"gopkg.in/typ.v4/slices"
)

type BranchRepository struct {
	db      *sql.DB
	tx      *dbtx.Transactor
	queries *sqlcPostgres.Queries
}

func NewBranchRepository(db *sql.DB) *BranchRepository {
	return &BranchRepository{db: db, tx: dbtx.NewTransactor(db), queries: sqlcPostgres.New(dbtx.New(db))}
}

func (b *BranchRepository) ListBranches(ctx context.Context, projectID snow.ID, limit int, updatedAfter *time.Time, lastID snow.ID) ([]*domain.Branch, error) {
	rows, err := b.queries.BranchList(ctx, sqlcPostgres.BranchListParams{
		ProjectID:     projectID.Int64(),
		Limit:         int64(limit),
		LastUpdatedAt: timePtrToNullTime(updatedAfter),
		LastID:        sql.NullInt64{Int64: lastID.Int64(), Valid: lastID != 0},
	})
	if err != nil {
		return nil, handleError(err)
	}

	return slices.Map(rows, func(b sqlcPostgres.Branch) *domain.Branch {
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
	row, err := b.queries.BranchGet(ctx, sqlcPostgres.BranchGetParams{
		ProjectID: projectID.Int64(),
		ID:        branchID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return branchToDomain(row), nil
}

func (b *BranchRepository) GetBranchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error) {
	row, err := b.queries.BranchGetByName(ctx, sqlcPostgres.BranchGetByNameParams{ProjectID: projectID.Int64(), Name: name})
	if err != nil {
		return nil, handleError(err)
	}
	return branchToDomain(row), nil
}

func (b *BranchRepository) CreateBranch(ctx context.Context, branch domain.Branch) (*domain.Branch, error) {
	var commitID sql.NullInt64
	if branch.CommitID != nil {
		commitID = sql.NullInt64{Int64: branch.CommitID.Int64(), Valid: true}
	}
	err := b.queries.BranchCreate(ctx, sqlcPostgres.BranchCreateParams{
		ID:          branch.ID.Int64(),
		ProjectID:   branch.ProjectID.Int64(),
		Name:        branch.Name,
		Key:         branch.Name,
		CommitID:    commitID,
		IsDefault:   branch.IsDefault,
		IsProtected: branch.IsProtected,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return b.GetBranchByName(ctx, branch.ProjectID, branch.Name)
}

func (b *BranchRepository) RenameBranch(ctx context.Context, projectID, branchID snow.ID, name, key string) error {
	err := b.queries.BranchSetName(ctx, sqlcPostgres.BranchSetNameParams{
		Name:      name,
		Key:       key,
		ProjectID: projectID.Int64(),
		ID:        branchID.Int64(),
	})
	return handleError(err)
}

func (b *BranchRepository) DeleteBranch(ctx context.Context, projectID, branchID snow.ID) error {
	affected, err := b.queries.BranchSoftDelete(ctx, sqlcPostgres.BranchSoftDeleteParams{
		ProjectID: projectID.Int64(),
		ID:        branchID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if affected == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return nil
}

func (b *BranchRepository) HasOpenMergeRequests(ctx context.Context, projectID, branchID snow.ID) (bool, error) {
	count, err := b.queries.MergeRequestCountOpenByBranch(ctx, sqlcPostgres.MergeRequestCountOpenByBranchParams{
		ProjectID: projectID.Int64(),
		Status:    domain.MergeRequestOpen,
		BranchID:  branchID.Int64(),
	})
	if err != nil {
		return false, handleError(err)
	}
	return count > 0, nil
}

func (b *BranchRepository) SetBranchProtection(ctx context.Context, projectID, branchID snow.ID, protection domain.BranchProtection) error {
	return b.tx.WithinTx(ctx, func(ctx context.Context) error {
		q := b.queries
		if err := q.BranchSetProtection(ctx, sqlcPostgres.BranchSetProtectionParams{
			IsProtected:           protection.Protected,
			RequiredApprovals:     protection.RequiredApprovals,
			DismissStaleApprovals: protection.DismissStaleApprovals,
			RequireStatusChecks:   protection.RequireStatusChecks,
			ProjectID:             projectID.Int64(),
			ID:                    branchID.Int64(),
		}); err != nil {
			return handleError(err)
		}
		if err := q.BranchRequiredReviewerClear(ctx, branchID.Int64()); err != nil {
			return handleError(err)
		}
		for _, reviewer := range protection.RequiredReviewers {
			if err := q.BranchRequiredReviewerAdd(ctx, sqlcPostgres.BranchRequiredReviewerAddParams{
				BranchID: branchID.Int64(),
				UserID:   reviewer.Int64(),
			}); err != nil {
				return handleError(err)
			}
		}
		if err := q.BranchRequiredCheckClear(ctx, branchID.Int64()); err != nil {
			return handleError(err)
		}
		for _, name := range protection.RequiredChecks {
			if err := q.BranchRequiredCheckAdd(ctx, sqlcPostgres.BranchRequiredCheckAddParams{
				BranchID: branchID.Int64(),
				Name:     name,
			}); err != nil {
				return handleError(err)
			}
		}
		return nil
	})
}

func (b *BranchRepository) RequiredReviewers(ctx context.Context, branchID snow.ID) ([]domain.ReviewActor, error) {
	rows, err := b.queries.BranchRequiredReviewerList(ctx, branchID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	reviewers := make([]domain.ReviewActor, 0, len(rows))
	for _, row := range rows {
		reviewers = append(reviewers, domain.ReviewActor{
			UserID:   snow.ID(row.UserID),
			Name:     row.UserName,
			PhotoURL: row.UserPhotoUrl.String,
		})
	}
	return reviewers, nil
}

func (b *BranchRepository) RequiredChecks(ctx context.Context, branchID snow.ID) ([]string, error) {
	names, err := b.queries.BranchRequiredCheckList(ctx, branchID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return names, nil
}

// branchIDBatches turns branch ids into the deduplicated id batches the
// *ListByBranches statements take.
func branchIDBatches(branchIDs []snow.ID) [][]int64 {
	ids := make([]int64, len(branchIDs))
	for i, id := range branchIDs {
		ids[i] = id.Int64()
	}
	return treeIDBatches(ids)
}

// RequiredReviewersByBranches is the batched RequiredReviewers: the required
// reviewers of every given branch keyed by branch id, each list ordered by
// user id. Branches without required reviewers have no entry.
func (b *BranchRepository) RequiredReviewersByBranches(ctx context.Context, branchIDs []snow.ID) (map[snow.ID][]domain.ReviewActor, error) {
	out := make(map[snow.ID][]domain.ReviewActor)
	for _, batch := range branchIDBatches(branchIDs) {
		rows, err := b.queries.BranchRequiredReviewerListByBranches(ctx, batch)
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range rows {
			branchID := snow.ID(row.BranchID)
			out[branchID] = append(out[branchID], domain.ReviewActor{
				UserID:   snow.ID(row.UserID),
				Name:     row.UserName,
				PhotoURL: row.UserPhotoUrl.String,
			})
		}
	}
	return out, nil
}

// RequiredChecksByBranches is the batched RequiredChecks: the required check
// names of every given branch keyed by branch id, each list ordered by name.
// Branches without required checks have no entry.
func (b *BranchRepository) RequiredChecksByBranches(ctx context.Context, branchIDs []snow.ID) (map[snow.ID][]string, error) {
	out := make(map[snow.ID][]string)
	for _, batch := range branchIDBatches(branchIDs) {
		rows, err := b.queries.BranchRequiredCheckListByBranches(ctx, batch)
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range rows {
			branchID := snow.ID(row.BranchID)
			out[branchID] = append(out[branchID], row.Name)
		}
	}
	return out, nil
}

func (b *BranchRepository) SetDefaultBranch(ctx context.Context, projectID, branchID snow.ID) error {
	return handleError(b.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := b.queries.BranchRemoveDefault(ctx, projectID.Int64()); err != nil {
			return err
		}
		return b.queries.BranchMarkDefault(ctx, sqlcPostgres.BranchMarkDefaultParams{
			ProjectID: projectID.Int64(),
			ID:        branchID.Int64(),
		})
	}))
}

func (b *BranchRepository) UpdateCommitIf(ctx context.Context, branchID snow.ID, fromCommitID, toCommitID *snow.ID) error {
	res, err := b.queries.BranchUpdateCommitIf(ctx, sqlcPostgres.BranchUpdateCommitIfParams{
		ID:           branchID.Int64(),
		FromCommitID: nullSnowID(fromCommitID),
		ToCommitID:   nullSnowID(toCommitID),
	})
	if err != nil {
		return handleError(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return handleError(err)
	}
	if n == 0 {
		return domain.NewErrorConflict("branch has moved; refresh and try again")
	}
	return nil
}

func (b *BranchRepository) GetCommit(ctx context.Context, commitID snow.ID) (*domain.Commit, error) {
	row, err := b.queries.CommitGet(ctx, commitID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return commitToDomain(row), nil
}

func (b *BranchRepository) CommitAncestors(ctx context.Context, projectID, startID snow.ID, limit int) ([]*domain.Commit, error) {
	rows, err := b.queries.CommitAncestors(ctx, sqlcPostgres.CommitAncestorsParams{
		ProjectID:     projectID.Int64(),
		StartCommitID: startID.Int64(),
		Limit:         int64(limit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, commitToDomain), nil
}

func (b *BranchRepository) GetCommitByHash(ctx context.Context, projectID snow.ID, hash domain.Hash) (*domain.Commit, error) {
	row, err := b.queries.CommitGetByHash(ctx, sqlcPostgres.CommitGetByHashParams{ProjectID: projectID.Int64(), Hash: hash.Bytes()})
	if err != nil {
		return nil, handleError(err)
	}
	return commitToDomain(row), nil
}

func (b *BranchRepository) CommitsByOrigins(ctx context.Context, projectID snow.ID, origins []string) ([]*domain.Commit, error) {
	if len(origins) == 0 {
		return nil, nil
	}
	rows, err := b.queries.CommitListByOrigins(ctx, sqlcPostgres.CommitListByOriginsParams{
		ProjectID: projectID.Int64(),
		Origins:   origins,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, commitToDomain), nil
}

func (b *BranchRepository) GetTreeNode(ctx context.Context, id int64) (*domain.TreeNode, error) {
	row, err := b.queries.TreeNodeGet(ctx, id)
	if err != nil {
		return nil, handleError(err)
	}
	return treeNodeToDomain(row), nil
}

func (b *BranchRepository) GetTreeChildByName(ctx context.Context, parentID int64, name string) (*domain.TreeNode, error) {
	row, err := b.queries.TreeNodeGetChildByName(ctx, sqlcPostgres.TreeNodeGetChildByNameParams{
		ParentTreeID: parentID,
		Name:         name,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return treeNodeToDomain(row), nil
}

func (b *BranchRepository) ListTreeChildren(ctx context.Context, parentID int64) ([]*domain.TreeNode, error) {
	rows, err := b.queries.TreeNodeListChildren(ctx, parentID)
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(t sqlcPostgres.TreeNode) *domain.TreeNode {
		return treeNodeToDomain(t)
	}), nil
}

// ListFileMetaByTree returns the files of one directory without their chunks.
func (b *BranchRepository) ListFileMetaByTree(ctx context.Context, treeID int64) ([]*domain.File, error) {
	rows, err := b.queries.FileListByTree(ctx, treeID)
	if err != nil {
		return nil, handleError(err)
	}
	files := make([]*domain.File, 0, len(rows))
	for _, row := range rows {
		files = append(files, fileToDomain(row))
	}
	return files, nil
}

func (b *BranchRepository) ListFilesByTree(ctx context.Context, treeID int64) ([]*domain.File, error) {
	rows, err := b.queries.FileListByTree(ctx, treeID)
	if err != nil {
		return nil, handleError(err)
	}
	files := make([]*domain.File, 0, len(rows))
	filesByID := make(map[int64]*domain.File, len(rows))
	for _, row := range rows {
		file := fileToDomain(row)
		files = append(files, file)
		filesByID[row.ID] = file
	}
	if len(files) == 0 {
		return files, nil
	}
	chunks, err := b.queries.ChunkListByTree(ctx, treeID)
	if err != nil {
		return nil, handleError(err)
	}
	for _, row := range chunks {
		file, ok := filesByID[row.FileID]
		if !ok {
			continue
		}
		file.Chunks = append(file.Chunks, chunkToDomain(sqlcPostgres.Chunk{
			ID:        row.ID,
			Hash:      row.Hash,
			SizeBytes: row.SizeBytes,
			CreatedAt: row.CreatedAt,
		}))
	}
	return files, nil
}

// treeBatchSize bounds how many tree node ids one batched tree statement
// carries. It stays far below SQLite's bound-parameter limit and keeps a
// single result set to a slice of a tree level on both dialects.
const treeBatchSize = 500

// ListTreeChildrenByTrees is the batched ListTreeChildren: it returns the
// child nodes of every requested node, keyed by the requested (ref) id and
// ordered by name. Nodes without children have no entry.
func (b *BranchRepository) ListTreeChildrenByTrees(ctx context.Context, parentIDs []int64) (map[int64][]*domain.TreeNode, error) {
	out := make(map[int64][]*domain.TreeNode)
	for _, batch := range treeIDBatches(parentIDs) {
		rows, err := b.queries.TreeNodeListChildrenByTrees(ctx, batch)
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range rows {
			out[row.RefID] = append(out[row.RefID], treeNodeToDomain(sqlcPostgres.TreeNode{
				ID:           row.ID,
				Hash:         row.Hash,
				Name:         row.Name,
				Mode:         row.Mode,
				ParentTreeID: row.ParentTreeID,
				CreatedAt:    row.CreatedAt,
			}))
		}
	}
	return out, nil
}

// ListFilesByTrees is the batched ListFilesByTree: it returns the files of
// every requested node with their chunk lists, keyed by the requested (ref)
// id, files ordered by name and chunks by chunk index. Nodes without files
// have no entry. Refs that resolve to the same content node each get their own
// File values and chunk slices.
func (b *BranchRepository) ListFilesByTrees(ctx context.Context, treeIDs []int64) (map[int64][]*domain.File, error) {
	out := make(map[int64][]*domain.File)
	for _, batch := range treeIDBatches(treeIDs) {
		rows, err := b.queries.FileListByTrees(ctx, batch)
		if err != nil {
			return nil, handleError(err)
		}
		if len(rows) == 0 {
			continue
		}
		// filesByID holds the first File of each file id; twins are the extra
		// copies handed to other refs that resolve to the same content node.
		filesByID := make(map[int64]*domain.File, len(rows))
		var twins map[int64][]*domain.File
		contentSeen := make(map[int64]struct{})
		var contentIDs []int64
		for _, row := range rows {
			file := fileToDomain(sqlcPostgres.File{
				ID:        row.ID,
				Name:      row.Name,
				Mode:      row.Mode,
				TreeID:    row.TreeID,
				Hash:      row.Hash,
				SizeBytes: row.SizeBytes,
				IsBinary:  row.IsBinary,
				Encoding:  row.Encoding,
				CreatedAt: row.CreatedAt,
			})
			out[row.RefID] = append(out[row.RefID], file)
			if _, ok := filesByID[row.ID]; !ok {
				filesByID[row.ID] = file
			} else {
				if twins == nil {
					twins = make(map[int64][]*domain.File)
				}
				twins[row.ID] = append(twins[row.ID], file)
			}
			if _, ok := contentSeen[row.TreeID.Int64]; !ok {
				contentSeen[row.TreeID.Int64] = struct{}{}
				contentIDs = append(contentIDs, row.TreeID.Int64)
			}
		}
		chunks, err := b.queries.ChunkListByContentTrees(ctx, contentIDs)
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range chunks {
			chunk := chunkToDomain(sqlcPostgres.Chunk{
				ID:        row.ID,
				Hash:      row.Hash,
				SizeBytes: row.SizeBytes,
				CreatedAt: row.CreatedAt,
			})
			file, ok := filesByID[row.FileID]
			if !ok {
				continue
			}
			file.Chunks = append(file.Chunks, chunk)
			for _, twin := range twins[row.FileID] {
				twin.Chunks = append(twin.Chunks, chunk)
			}
		}
	}
	return out, nil
}

// ListFileMetaByTrees is the batched ListFileMetaByTree: the files of every
// requested node without their chunks, keyed by the requested (ref) id and
// ordered by name. Nodes without files have no entry, and refs that resolve to
// the same content node each get their own File values.
func (b *BranchRepository) ListFileMetaByTrees(ctx context.Context, treeIDs []int64) (map[int64][]*domain.File, error) {
	out := make(map[int64][]*domain.File)
	for _, batch := range treeIDBatches(treeIDs) {
		rows, err := b.queries.FileListByTrees(ctx, batch)
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range rows {
			out[row.RefID] = append(out[row.RefID], fileToDomain(sqlcPostgres.File{
				ID:        row.ID,
				Name:      row.Name,
				Mode:      row.Mode,
				TreeID:    row.TreeID,
				Hash:      row.Hash,
				SizeBytes: row.SizeBytes,
				IsBinary:  row.IsBinary,
				Encoding:  row.Encoding,
				CreatedAt: row.CreatedAt,
			}))
		}
	}
	return out, nil
}

// ListChunksByFiles returns the chunk lists of the given files in chunk-index
// order, keyed by file id. Files without chunks (and unknown ids) have no
// entry.
func (b *BranchRepository) ListChunksByFiles(ctx context.Context, fileIDs []int64) (map[int64][]domain.Chunk, error) {
	out := make(map[int64][]domain.Chunk)
	for _, batch := range treeIDBatches(fileIDs) {
		rows, err := b.queries.ChunkListByFiles(ctx, batch)
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range rows {
			out[row.FileID] = append(out[row.FileID], chunkToDomain(sqlcPostgres.Chunk{
				ID:        row.ID,
				Hash:      row.Hash,
				SizeBytes: row.SizeBytes,
				CreatedAt: row.CreatedAt,
			}))
		}
	}
	return out, nil
}

// treeIDBatches drops duplicate ids and splits the rest into treeBatchSize
// slices, keeping first-seen order.
func treeIDBatches(ids []int64) [][]int64 {
	seen := make(map[int64]struct{}, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	var batches [][]int64
	for start := 0; start < len(unique); start += treeBatchSize {
		batches = append(batches, unique[start:min(start+treeBatchSize, len(unique))])
	}
	return batches
}

func (b *BranchRepository) CommitLog(ctx context.Context, projectID snow.ID, startCommitID snow.ID, limit int) ([]*domain.CommitLogEntry, error) {
	rows, err := b.queries.CommitLog(ctx, sqlcPostgres.CommitLogParams{
		ProjectID:     projectID.Int64(),
		StartCommitID: startCommitID.Int64(),
		Limit:         int64(limit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(row sqlcPostgres.CommitLogRow) *domain.CommitLogEntry {
		return commitLogEntryToDomain(row)
	}), nil
}

func commitLogEntryToDomain(row sqlcPostgres.CommitLogRow) *domain.CommitLogEntry {
	return &domain.CommitLogEntry{
		Commit: domain.Commit{
			ID:        snow.ID(row.ID),
			Hash:      bytesToHash(row.Hash),
			ProjectID: snow.ID(row.ProjectID),
			TreeID:    row.TreeID,
			Parent1ID: nullInt64SnowIDPtr(row.Parent1ID),
			Parent2ID: nullInt64SnowIDPtr(row.Parent2ID),
			UserID:    snow.ID(row.UserID),
			Message:   row.Message,
			CreatedAt: row.CreatedAt,

			AuthorName:  row.CommitAuthorName.String,
			AuthorEmail: row.CommitAuthorEmail.String,
			Origin:      row.Origin.String,
		},
		AuthorName:  row.AuthorName,
		AuthorEmail: row.AuthorEmail,
	}
}

func (b *BranchRepository) CommitLogUntil(ctx context.Context, projectID snow.ID, startCommitID, stopCommitID snow.ID, limit int) ([]*domain.CommitLogEntry, error) {
	rows, err := b.queries.CommitLogUntil(ctx, sqlcPostgres.CommitLogUntilParams{
		ProjectID:     projectID.Int64(),
		StartCommitID: startCommitID.Int64(),
		StopCommitID:  stopCommitID.Int64(),
		Limit:         int64(limit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(row sqlcPostgres.CommitLogUntilRow) *domain.CommitLogEntry {
		return commitLogUntilEntryToDomain(row)
	}), nil
}

func commitLogUntilEntryToDomain(row sqlcPostgres.CommitLogUntilRow) *domain.CommitLogEntry {
	return &domain.CommitLogEntry{
		Commit: domain.Commit{
			ID:        snow.ID(row.ID),
			Hash:      bytesToHash(row.Hash),
			ProjectID: snow.ID(row.ProjectID),
			TreeID:    row.TreeID,
			Parent1ID: nullInt64SnowIDPtr(row.Parent1ID),
			Parent2ID: nullInt64SnowIDPtr(row.Parent2ID),
			UserID:    snow.ID(row.UserID),
			Message:   row.Message,
			CreatedAt: row.CreatedAt,

			AuthorName:  row.CommitAuthorName.String,
			AuthorEmail: row.CommitAuthorEmail.String,
			Origin:      row.Origin.String,
		},
		AuthorName:  row.AuthorName,
		AuthorEmail: row.AuthorEmail,
	}
}

func branchToDomain(b sqlcPostgres.Branch) *domain.Branch {
	var commitID *snow.ID
	if b.CommitID.Valid {
		id := snow.ID(b.CommitID.Int64)
		commitID = &id
	}
	return &domain.Branch{
		ID:                    snow.ID(b.ID),
		ProjectID:             snow.ID(b.ProjectID),
		Name:                  b.Name,
		IsProtected:           b.IsProtected,
		RequiredApprovals:     b.RequiredApprovals,
		DismissStaleApprovals: b.DismissStaleApprovals,
		RequireStatusChecks:   b.RequireStatusChecks,
		IsDefault:             b.IsDefault,
		CommitID:              commitID,
		UpdatedAt:             b.UpdatedAt,
		CreatedAt:             b.CreatedAt,
		Deleted:               b.Deleted,
		DeletedAt:             nullTimePtr(b.DeletedAt),
	}
}

func commitToDomain(c sqlcPostgres.Commit) *domain.Commit {
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

		AuthorName:  c.AuthorName.String,
		AuthorEmail: c.AuthorEmail.String,
		Origin:      c.Origin.String,
	}
}

func treeNodeToDomain(t sqlcPostgres.TreeNode) *domain.TreeNode {
	return &domain.TreeNode{
		ID:        t.ID,
		Hash:      bytesToHash(t.Hash),
		Name:      t.Name,
		Mode:      int(t.Mode),
		ParentID:  nullInt64Ptr(t.ParentTreeID),
		CreatedAt: t.CreatedAt.Time,
	}
}

func fileToDomain(f sqlcPostgres.File) *domain.File {
	return &domain.File{
		ID:        f.ID,
		Hash:      bytesToHash(f.Hash),
		Name:      f.Name,
		Mode:      int(f.Mode),
		TreeID:    f.TreeID.Int64,
		SizeBytes: f.SizeBytes,
		IsBinary:  f.IsBinary,
		Encoding:  f.Encoding,
		CreatedAt: f.CreatedAt.Time,
	}
}

func chunkToDomain(c sqlcPostgres.Chunk) domain.Chunk {
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
