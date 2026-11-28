package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/repository/pushbatch"
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

// InsertChunksIfNotExist records loose chunk rows (InsertChunkRows).
func (p *PushRepository) InsertChunksIfNotExist(ctx context.Context, projectID snow.ID, chunks []usecase.ChunkRef) error {
	return p.InsertChunkRows(ctx, projectID, pushbatch.LooseRows(chunks))
}

// InsertChunkRows records the project's chunk rows for chunks confirm (or a
// server-side write) verified, at their locations (a pack and offset, or
// loose), together with their upload markers (project_chunk_uploads, which
// keep the garbage collector away from chunks a push has not committed
// yet), in one statement per batch each. A chunk row is the project's link to
// the content: rows that already exist are left alone and keep their
// location. It joins the transaction the context carries, if any.
func (p *PushRepository) InsertChunkRows(ctx context.Context, projectID snow.ID, rows []usecase.ChunkRow) error {
	q := sqlcSqlite.New(dbtx.New(p.db))
	err := pushbatch.InsertChunkRows(ctx, rows, func(ctx context.Context, payload []byte) error {
		return handleError(q.ChunkInsertManyOrIgnore(ctx, sqlcSqlite.ChunkInsertManyOrIgnoreParams{
			ProjectID: projectID.Int64(),
			ChunkRows: string(payload),
		}))
	})
	if err != nil {
		return err
	}
	hashes := make([]domain.Hash, 0, len(rows))
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}
	return pushbatch.InsertHashes(ctx, hashes, func(ctx context.Context, payload []byte) error {
		return handleError(q.ProjectChunkUploadInsertMany(ctx, sqlcSqlite.ProjectChunkUploadInsertManyParams{
			ProjectID: projectID.Int64(),
			Hashes:    string(payload),
		}))
	})
}

// ProjectChunkSizes returns the stored size of every hash the project has a
// chunk row for; other hashes are absent. It joins the transaction the
// context carries, if any.
func (p *PushRepository) ProjectChunkSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error) {
	q := sqlcSqlite.New(dbtx.New(p.db))
	sizes := make(map[domain.Hash]int64, len(hashes))
	err := pushbatch.LookupBatches(hashes, func(batch [][]byte) error {
		rows, err := q.ProjectChunkSizes(ctx, sqlcSqlite.ProjectChunkSizesParams{
			ProjectID: projectID.Int64(),
			Hashes:    batch,
		})
		if err != nil {
			return handleError(err)
		}
		for _, row := range rows {
			hash, err := domain.HashFromBytes(row.Hash)
			if err != nil {
				return domain.NewErrorDatabase(err.Error())
			}
			sizes[hash] = row.SizeBytes
		}
		return nil
	})
	if err != nil {
		return nil, err
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

	q := sqlcSqlite.New(tx)
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

func applyPush(ctx context.Context, q *sqlcSqlite.Queries, req usecase.ApplyPushRequest) error {
	if err := insertCommit(ctx, q, req); err != nil {
		return err
	}
	if req.BranchID == 0 {
		return nil
	}
	return handleError(q.BranchUpdateCommit(ctx, sqlcSqlite.BranchUpdateCommitParams{
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
		if existing, lookupErr := commitByOrigin(ctx, sqlcSqlite.New(p.db), req); lookupErr == nil && existing != nil {
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

	q := sqlcSqlite.New(tx)
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

func commitByOrigin(ctx context.Context, q *sqlcSqlite.Queries, req usecase.ApplyPushRequest) (*domain.Commit, error) {
	row, err := q.CommitGetByOrigin(ctx, sqlcSqlite.CommitGetByOriginParams{
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

func insertCommit(ctx context.Context, q *sqlcSqlite.Queries, req usecase.ApplyPushRequest) error {
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

	if err := pushbatch.InsertFiles(ctx, batch, req.Files, nodeIDs, chunkIDs); err != nil {
		return err
	}

	rootID := nodeIDs[req.Nodes[0].ID]
	if err := q.CommitInsert(ctx, sqlcSqlite.CommitInsertParams{
		ID:        req.CommitID.Int64(),
		Hash:      req.CommitHash.Bytes(),
		ProjectID: req.ProjectID.Int64(),
		TreeID:    rootID,
		Parent1ID: nullID(req.ParentID),
		Parent2ID: nullID(req.ParentID2),
		UserID:    req.UserID.Int64(),
		Message:   req.Message,

		AuthorName:  optionalString(req.AuthorName),
		AuthorEmail: optionalString(req.AuthorEmail),
		CreatedAt:   commitCreatedAt(req.CreatedAt),
		Origin:      optionalString(req.Origin),
	}); err != nil {
		return handleError(err)
	}
	if !req.IndexPaths {
		return nil
	}
	return writePathIndex(ctx, q, req.CommitID, req.PathChanges)
}

func nullID(id *snow.ID) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id.Int64(), Valid: true}
}

// pushBatchStore runs the bulk statements of pushbatch against sqlite.
type pushBatchStore struct {
	q         *sqlcSqlite.Queries
	projectID int64
}

func (s pushBatchStore) ChunkIDs(ctx context.Context, hashes [][]byte) (map[domain.Hash]int64, error) {
	rows, err := s.q.ChunkListByHashesForProject(ctx, sqlcSqlite.ChunkListByHashesForProjectParams{
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
	rows, err := s.q.FileInsertMany(ctx, string(payload))
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
	return handleError(s.q.FileChunkInsertMany(ctx, string(payload)))
}

func commitCreatedAt(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.DateTime)
}

// LockProject is a no-op: sqlite write transactions are immediate and already
// serialize writers.
func (p *PushRepository) LockProject(context.Context, snow.ID) error {
	return nil
}
