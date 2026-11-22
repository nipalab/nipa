package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// TreeHashRepository implements usecase.TreeHashRepository. Every query goes
// through dbtx, so the ones a backfill pass runs join its transaction.
type TreeHashRepository struct {
	tx      *dbtx.Transactor
	queries *sqlcSqlite.Queries
}

var _ usecase.TreeHashRepository = (*TreeHashRepository)(nil)

func NewTreeHashRepository(db *sql.DB) *TreeHashRepository {
	return &TreeHashRepository{tx: dbtx.NewTransactor(db), queries: sqlcSqlite.New(dbtx.New(db))}
}

// WithTreeHashBackfillLock runs fn in one transaction. The server opens SQLite
// with immediate transactions, so the pass holds the write lock throughout and
// no write can commit below its watermark; the overlap window is still applied
// so both dialects behave the same. The marker is always written: started_at
// moves the window forward even when nothing was scanned.
func (r *TreeHashRepository) WithTreeHashBackfillLock(ctx context.Context, fn func(ctx context.Context, state usecase.TreeHashBackfillState) (int64, error)) error {
	return r.tx.WithinTx(ctx, func(ctx context.Context) error {
		watermark, err := r.queries.TreeHashBackfillWatermark(ctx)
		if err != nil {
			return handleError(err)
		}
		rescanFrom, err := r.queries.TreeHashBackfillRescanFrom(ctx, int64(usecase.TreeHashBackfillOverlap/time.Second))
		if err != nil {
			return handleError(err)
		}
		next, err := fn(ctx, usecase.TreeHashBackfillState{Watermark: watermark, RescanFrom: rescanFrom})
		if err != nil {
			return err
		}
		return handleError(r.queries.TreeHashBackfillSetWatermark(ctx, next))
	})
}

func (r *TreeHashRepository) MaxTreeNodeID(ctx context.Context) (int64, error) {
	id, err := r.queries.TreeNodeMaxID(ctx)
	return id, handleError(err)
}

func (r *TreeHashRepository) TreeHashSeeds(ctx context.Context, afterID, upToID int64) ([]int64, error) {
	ids, err := r.queries.TreeHashSeeds(ctx, sqlcSqlite.TreeHashSeedsParams{
		AfterID: sql.NullInt64{Int64: afterID, Valid: true},
		UpToID:  sql.NullInt64{Int64: upToID, Valid: true},
	})
	return ids, handleError(err)
}

func (r *TreeHashRepository) TreeHashNode(ctx context.Context, id int64) (*usecase.TreeHashNode, error) {
	row, err := r.queries.TreeHashNode(ctx, id)
	if err != nil {
		return nil, handleError(err)
	}
	return &usecase.TreeHashNode{
		ID:         row.ID,
		ParentID:   nullInt64Ptr(row.ParentTreeID),
		Name:       row.Name,
		Hash:       bytesToHash(row.Hash),
		HasContent: row.HasContent != 0,
	}, nil
}

func (r *TreeHashRepository) TreeHashFiles(ctx context.Context, treeID int64) ([]treehash.FileEntry, error) {
	rows, err := r.queries.TreeHashFiles(ctx, sql.NullInt64{Int64: treeID, Valid: true})
	if err != nil {
		return nil, handleError(err)
	}
	files := make([]treehash.FileEntry, 0, len(rows))
	for _, row := range rows {
		files = append(files, treehash.FileEntry{Name: row.Name, Hash: bytesToHash(row.Hash), Mode: int(row.Mode)})
	}
	return files, nil
}

func (r *TreeHashRepository) TreeHashChildren(ctx context.Context, treeID int64) ([]usecase.TreeHashNode, error) {
	rows, err := r.queries.TreeHashChildren(ctx, sql.NullInt64{Int64: treeID, Valid: true})
	if err != nil {
		return nil, handleError(err)
	}
	nodes := make([]usecase.TreeHashNode, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, usecase.TreeHashNode{
			ID:       row.ID,
			ParentID: nullInt64Ptr(row.ParentTreeID),
			Name:     row.Name,
			Hash:     bytesToHash(row.Hash),
		})
	}
	return nodes, nil
}

func (r *TreeHashRepository) TreeHashNodesByHash(ctx context.Context, hash domain.Hash) ([]usecase.TreeHashNode, error) {
	rows, err := r.queries.TreeHashNodesByHash(ctx, hash.Bytes())
	if err != nil {
		return nil, handleError(err)
	}
	nodes := make([]usecase.TreeHashNode, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, usecase.TreeHashNode{
			ID:         row.ID,
			ParentID:   nullInt64Ptr(row.ParentTreeID),
			Name:       row.Name,
			Hash:       bytesToHash(row.Hash),
			HasContent: row.HasContent != 0,
		})
	}
	return nodes, nil
}

func (r *TreeHashRepository) TreeHashDanglingReferences(ctx context.Context, afterID, upToID int64) ([]usecase.TreeHashDanglingReference, error) {
	rows, err := r.queries.TreeHashDanglingReferences(ctx, sqlcSqlite.TreeHashDanglingReferencesParams{AfterID: afterID, UpToID: upToID})
	if err != nil {
		return nil, handleError(err)
	}
	refs := make([]usecase.TreeHashDanglingReference, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, usecase.TreeHashDanglingReference{
			Node: usecase.TreeHashNode{
				ID:       row.ID,
				ParentID: nullInt64Ptr(row.ParentTreeID),
				Name:     row.Name,
				Hash:     bytesToHash(row.Hash),
			},
			NewHash: bytesToHash(row.NewHash),
		})
	}
	return refs, nil
}

func (r *TreeHashRepository) SaveTreeHashRewrite(ctx context.Context, oldHash, newHash domain.Hash) error {
	return handleError(r.queries.TreeHashRewriteInsert(ctx, sqlcSqlite.TreeHashRewriteInsertParams{OldHash: oldHash.Bytes(), NewHash: newHash.Bytes()}))
}

func (r *TreeHashRepository) SetTreeNodeHash(ctx context.Context, id int64, hash domain.Hash) error {
	return handleError(r.queries.TreeNodeSetHash(ctx, sqlcSqlite.TreeNodeSetHashParams{Hash: hash.Bytes(), ID: id}))
}

func (r *TreeHashRepository) SaveLegacyTreeHash(ctx context.Context, treeID int64, hash domain.Hash) error {
	return handleError(r.queries.TreeHashLegacyInsert(ctx, sqlcSqlite.TreeHashLegacyInsertParams{TreeID: treeID, Hash: hash.Bytes()}))
}

func (r *TreeHashRepository) LegacyTreeHash(ctx context.Context, treeID int64) (*domain.Hash, error) {
	raw, err := r.queries.TreeHashLegacyGet(ctx, treeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, handleError(err)
	}
	hash := bytesToHash(raw)
	return &hash, nil
}
