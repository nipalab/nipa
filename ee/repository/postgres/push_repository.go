package postgres

import (
	"context"
	"database/sql"
	"errors"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
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

// InsertChunksIfNotExist records the metadata rows of chunks confirm verified,
// in one statement per batch; existing rows and duplicates are left alone. It
// joins the transaction the context carries, if any.
func (p *PushRepository) InsertChunksIfNotExist(ctx context.Context, chunks []usecase.ChunkRef) error {
	q := sqlcPostgres.New(dbtx.New(p.db))
	return pushbatch.InsertChunkRows(ctx, chunks, func(ctx context.Context, payload []byte) error {
		return handleError(q.ChunkInsertManyOrIgnore(ctx, payload))
	})
}

// LinkProjectChunks links the project to the chunks in one statement per
// batch; links that already exist are left alone. It joins the transaction
// the context carries, if any.
func (p *PushRepository) LinkProjectChunks(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error {
	q := sqlcPostgres.New(dbtx.New(p.db))
	return pushbatch.InsertHashes(ctx, hashes, func(ctx context.Context, payload []byte) error {
		return handleError(q.ProjectChunkUploadInsertMany(ctx, sqlcPostgres.ProjectChunkUploadInsertManyParams{
			ProjectID: projectID.Int64(),
			Hashes:    payload,
		}))
	})
}

// KnownChunks reports which hashes have a chunk metadata row, whatever project
// they are linked to.
func (p *PushRepository) KnownChunks(ctx context.Context, hashes []domain.Hash) (map[domain.Hash]struct{}, error) {
	q := sqlcPostgres.New(dbtx.New(p.db))
	known := make(map[domain.Hash]struct{}, len(hashes))
	err := pushbatch.LookupBatches(hashes, func(batch [][]byte) error {
		rows, err := q.ChunkKnownHashes(ctx, batch)
		if err != nil {
			return handleError(err)
		}
		for _, row := range rows {
			hash, err := domain.HashFromBytes(row)
			if err != nil {
				return domain.NewErrorDatabase(err.Error())
			}
			known[hash] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return known, nil
}

const linkedChunkBatch = 500

func (p *PushRepository) LinkedProjectChunkSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error) {
	q := sqlcPostgres.New(p.db)
	sizes := make(map[domain.Hash]int64, len(hashes))
	for start := 0; start < len(hashes); start += linkedChunkBatch {
		end := min(start+linkedChunkBatch, len(hashes))
		batch := make([][]byte, 0, end-start)
		for _, hash := range hashes[start:end] {
			batch = append(batch, hash.Bytes())
		}
		rows, err := q.ProjectChunkLinkedSizes(ctx, sqlcPostgres.ProjectChunkLinkedSizesParams{
			ProjectID: projectID.Int64(),
			Hashes:    batch,
		})
		if err != nil {
			return nil, handleError(err)
		}
		for _, row := range rows {
			hash, err := domain.HashFromBytes(row.Hash)
			if err != nil {
				return nil, domain.NewErrorDatabase(err.Error())
			}
			sizes[hash] = row.SizeBytes
		}
	}
	return sizes, nil
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
	if err := insertCommit(ctx, q, req); err != nil {
		return err
	}
	if req.BranchID == 0 {
		return nil
	}
	return handleError(q.BranchUpdateCommit(ctx, sqlcPostgres.BranchUpdateCommitParams{
		CommitID: sql.NullInt64{Int64: req.CommitID.Int64(), Valid: true},
		ID:       req.BranchID.Int64(),
	}))
}

func (p *PushRepository) ApplyImport(ctx context.Context, req usecase.ApplyPushRequest) (*domain.Commit, error) {
	if req.Origin == "" {
		return nil, domain.NewErrorInternalServer("import commit has no origin")
	}
	commit, err := p.applyImport(ctx, req)
	if err != nil && domain.IsErrorConflict(err) {
		if existing, lookupErr := commitByOrigin(ctx, sqlcPostgres.New(p.db), req); lookupErr == nil && existing != nil {
			return existing, nil
		}
	}
	return commit, err
}

func (p *PushRepository) applyImport(ctx context.Context, req usecase.ApplyPushRequest) (*domain.Commit, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, handleError(err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcPostgres.New(tx)
	existing, err := commitByOrigin(ctx, q, req)
	if err != nil || existing != nil {
		return existing, err
	}
	if err := insertCommit(ctx, q, req); err != nil {
		return nil, err
	}
	row, err := q.CommitGet(ctx, req.CommitID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, handleError(err)
	}
	return commitToDomain(row), nil
}

func commitByOrigin(ctx context.Context, q *sqlcPostgres.Queries, req usecase.ApplyPushRequest) (*domain.Commit, error) {
	row, err := q.CommitGetByOrigin(ctx, sqlcPostgres.CommitGetByOriginParams{
		ProjectID: req.ProjectID.Int64(),
		Origin:    sql.NullString{String: req.Origin, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, handleError(err)
	}
	return commitToDomain(row), nil
}

func insertCommit(ctx context.Context, q *sqlcPostgres.Queries, req usecase.ApplyPushRequest) error {
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

		AuthorName:  optionalString(req.AuthorName),
		AuthorEmail: optionalString(req.AuthorEmail),
		CreatedAt:   timePtrToNullTime(req.CreatedAt),
		Origin:      optionalString(req.Origin),
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

// importAdvisoryNamespace keeps import ref locks apart from the other
// per-project advisory locks ("NIPIMPT").
const importAdvisoryNamespace int64 = 0x4e4950494d5054

// LockProject takes the project's transaction-scoped import lock. It must run
// inside a transaction carried by ctx.
func (p *PushRepository) LockProject(ctx context.Context, projectID snow.ID) error {
	_, err := dbtx.New(p.db).ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, projectID.Int64()^importAdvisoryNamespace)
	return handleError(err)
}
