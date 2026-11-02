package postgres

import (
	"context"
	"database/sql"
	"errors"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/pushbatch"
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
	q := sqlcPostgres.New(p.db)
	return handleError(q.ChunkInsertOrIgnore(ctx, sqlcPostgres.ChunkInsertOrIgnoreParams{
		Hash:      hash.Bytes(),
		SizeBytes: sizeBytes,
	}))
}

func (p *PushRepository) HasChunk(ctx context.Context, hash domain.Hash) (bool, error) {
	q := sqlcPostgres.New(p.db)
	if _, err := q.ChunkGetByHash(ctx, hash.Bytes()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, handleError(err)
	}
	return true, nil
}

func (p *PushRepository) LinkProjectChunk(ctx context.Context, projectID snow.ID, hash domain.Hash) error {
	q := sqlcPostgres.New(p.db)
	return handleError(q.ProjectChunkUploadInsert(ctx, sqlcPostgres.ProjectChunkUploadInsertParams{
		ProjectID: projectID.Int64(),
		Hash:      hash.Bytes(),
	}))
}

func (p *PushRepository) LinkProjectChunks(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error {
	if len(hashes) == 0 {
		return nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return handleError(err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcPostgres.New(tx)
	for _, hash := range hashes {
		if err := q.ProjectChunkUploadInsert(ctx, sqlcPostgres.ProjectChunkUploadInsertParams{
			ProjectID: projectID.Int64(),
			Hash:      hash.Bytes(),
		}); err != nil {
			return handleError(err)
		}
	}
	return handleError(tx.Commit())
}

const linkedChunkBatch = 500

func (p *PushRepository) LinkedProjectChunks(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]bool, error) {
	q := sqlcPostgres.New(p.db)
	linked := make(map[domain.Hash]bool, len(hashes))
	for start := 0; start < len(hashes); start += linkedChunkBatch {
		end := min(start+linkedChunkBatch, len(hashes))
		batch := make([][]byte, 0, end-start)
		for _, hash := range hashes[start:end] {
			batch = append(batch, hash.Bytes())
		}
		rows, err := q.ProjectChunkUploadList(ctx, sqlcPostgres.ProjectChunkUploadListParams{
			ProjectID: projectID.Int64(),
			Hashes:    batch,
		})
		if err != nil {
			return nil, handleError(err)
		}
		for _, raw := range rows {
			hash, err := domain.HashFromBytes(raw)
			if err != nil {
				return nil, domain.NewErrorDatabase(err.Error())
			}
			linked[hash] = true
		}
	}
	return linked, nil
}

func (p *PushRepository) ApplyPush(ctx context.Context, req usecase.ApplyPushRequest) error {
	return p.ApplyPushAll(ctx, []usecase.ApplyPushRequest{req})
}

// ApplyPushAll applies one or more commits in a single transaction. A rebase
// replays several commits, and either the whole chain lands or none of it does.
func (p *PushRepository) ApplyPushAll(ctx context.Context, reqs []usecase.ApplyPushRequest) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return handleError(err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcPostgres.New(tx)
	for _, req := range reqs {
		if err := applyPush(ctx, q, req); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return handleError(err)
	}
	return nil
}

func applyPush(ctx context.Context, q *sqlcPostgres.Queries, req usecase.ApplyPushRequest) error {
	batch := pushBatchStore{q: q, projectID: req.ProjectID.Int64()}
	chunkIDs, err := pushbatch.ResolveChunks(ctx, batch, req.Files)
	if err != nil {
		return err
	}

	nodeIDs := map[int64]int64{}
	for _, n := range req.Nodes {
		var parent sql.NullInt64
		if n.ParentID != nil {
			parent = sql.NullInt64{Int64: nodeIDs[*n.ParentID], Valid: true}
		}
		id, err := q.TreeNodeInsert(ctx, sqlcPostgres.TreeNodeInsertParams{
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

	if err := pushbatch.InsertFiles(ctx, batch, req.Files, nodeIDs, chunkIDs); err != nil {
		return err
	}

	rootID := nodeIDs[req.Nodes[0].ID]
	if err := q.CommitInsert(ctx, sqlcPostgres.CommitInsertParams{
		ID:        req.CommitID.Int64(),
		Hash:      req.CommitHash.Bytes(),
		ProjectID: req.ProjectID.Int64(),
		TreeID:    rootID,
		Parent1ID: nullSnowID(req.ParentID),
		Parent2ID: nullSnowID(req.ParentID2),
		UserID:    req.UserID.Int64(),
		Message:   req.Message,
	}); err != nil {
		return handleError(err)
	}

	if err := q.BranchUpdateCommit(ctx, sqlcPostgres.BranchUpdateCommitParams{
		CommitID: sql.NullInt64{Int64: req.CommitID.Int64(), Valid: true},
		ID:       req.BranchID.Int64(),
	}); err != nil {
		return handleError(err)
	}
	return nil
}

// pushBatchStore runs the bulk statements of pushbatch against postgres.
type pushBatchStore struct {
	q         *sqlcPostgres.Queries
	projectID int64
}

func (s pushBatchStore) ChunkIDs(ctx context.Context, hashes [][]byte) (map[domain.Hash]int64, error) {
	rows, err := s.q.ChunkListByHashesForProject(ctx, sqlcPostgres.ChunkListByHashesForProjectParams{
		ProjectID: s.projectID,
		Hashes:    hashes,
	})
	if err != nil {
		return nil, handleError(err)
	}
	ids := make(map[domain.Hash]int64, len(rows))
	for _, row := range rows {
		hash, err := domain.HashFromBytes(row.Hash)
		if err != nil {
			return nil, domain.NewErrorDatabase(err.Error())
		}
		ids[hash] = row.ID
	}
	return ids, nil
}

func (s pushBatchStore) InsertFiles(ctx context.Context, payload []byte) (map[pushbatch.FileKey]int64, error) {
	rows, err := s.q.FileInsertMany(ctx, payload)
	if err != nil {
		return nil, handleError(err)
	}
	ids := make(map[pushbatch.FileKey]int64, len(rows))
	for _, row := range rows {
		ids[pushbatch.FileKey{TreeID: row.TreeID.Int64, Name: row.Name}] = row.ID
	}
	return ids, nil
}

func (s pushBatchStore) InsertLinks(ctx context.Context, payload []byte) error {
	return handleError(s.q.FileChunkInsertMany(ctx, payload))
}
