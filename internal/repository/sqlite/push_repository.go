package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type PushRepository struct {
	db *sql.DB
}

func NewPushRepository(db *sql.DB) *PushRepository {
	return &PushRepository{db: db}
}

func (p *PushRepository) InsertChunkIfNotExists(ctx context.Context, hash domain.Hash, sizeBytes int64) error {
	q := sqlcSqlite.New(p.db)
	return handleError(q.ChunkInsertOrIgnore(ctx, sqlcSqlite.ChunkInsertOrIgnoreParams{
		Hash:      hash.Bytes(),
		SizeBytes: sizeBytes,
	}))
}

func (p *PushRepository) ApplyPush(ctx context.Context, req usecase.ApplyPushRequest) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return handleError(err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcSqlite.New(tx)

	chunkIDs := map[domain.Hash]int64{}
	ensureChunk := func(hash domain.Hash) error {
		if _, ok := chunkIDs[hash]; ok {
			return nil
		}
		row, err := q.ChunkGetByHash(ctx, hash.Bytes())
		if err != nil {
			if err == sql.ErrNoRows {
				return domain.NewErrorUser("missing chunk data for " + hash.String())
			}
			return handleError(err)
		}
		chunkIDs[hash] = row.ID
		return nil
	}
	for _, f := range req.Files {
		for _, ch := range f.ChunkHashes {
			if err := ensureChunk(ch); err != nil {
				return err
			}
		}
	}

	nodeIDs := map[int64]int64{}
	for _, n := range req.Nodes {
		var parent sql.NullInt64
		if n.ParentID != nil {
			parent = sql.NullInt64{Int64: nodeIDs[*n.ParentID], Valid: true}
		}
		id, err := q.TreeNodeInsert(ctx, sqlcSqlite.TreeNodeInsertParams{
			Hash:         n.Hash.Bytes(),
			Name:         n.Name,
			Mode:         int64(n.Mode),
			ParentTreeID: parent,
		})
		if err != nil {
			return handleError(err)
		}
		nodeIDs[n.ID] = id
	}
	if len(req.Nodes) == 0 {
		return domain.NewErrorDatabase("push produced no tree nodes")
	}

	for _, f := range req.Files {
		fileID, err := q.FileInsert(ctx, sqlcSqlite.FileInsertParams{
			Name:      f.Name,
			Mode:      int64(f.Mode),
			TreeID:    sql.NullInt64{Int64: nodeIDs[f.TreeID], Valid: true},
			Hash:      f.Hash.Bytes(),
			SizeBytes: f.SizeBytes,
			IsBinary:  f.IsBinary,
		})
		if err != nil {
			return handleError(err)
		}
		for index, ch := range f.ChunkHashes {
			if err := q.FileChunkInsert(ctx, sqlcSqlite.FileChunkInsertParams{
				FileID:     fileID,
				ChunkID:    chunkIDs[ch],
				ChunkIndex: int64(index),
			}); err != nil {
				return handleError(err)
			}
		}
	}

	rootID := nodeIDs[req.Nodes[0].ID]
	if err := q.CommitInsert(ctx, sqlcSqlite.CommitInsertParams{
		ID:        req.CommitID.Int64(),
		Hash:      req.CommitHash.Bytes(),
		ProjectID: req.ProjectID.Int64(),
		TreeID:    rootID,
		Parent1ID: nullID(req.ParentID),
		UserID:    req.UserID.Int64(),
		Message:   req.Message,
	}); err != nil {
		return handleError(err)
	}

	if err := q.BranchUpdateCommit(ctx, sqlcSqlite.BranchUpdateCommitParams{
		CommitID: sql.NullInt64{Int64: req.CommitID.Int64(), Valid: true},
		ID:       req.BranchID.Int64(),
	}); err != nil {
		return handleError(err)
	}

	if err := tx.Commit(); err != nil {
		return handleError(err)
	}
	return nil
}

func nullID(id *snow.ID) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id.Int64(), Valid: true}
}
