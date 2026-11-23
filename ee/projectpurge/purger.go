// Package projectpurge removes deleted projects for good once their restore
// window has passed: their history rows (commits, trees, files, file chunk
// links), refs, merge requests, chunk rows, pack rows and upload markers,
// the chunk and pack objects in the server's own store (prefix deletes of
// p/<project>/ and packs/<project>/: chunks and packs belong to one project)
// and the project row, which frees the slug. A project on its own
// storage (BYO) keeps its objects: the customer's bucket is never touched.
//
// Enterprise Edition: see ee/LICENSE.
package projectpurge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const (
	// DefaultBatchSize is the number of commits removed per transaction.
	DefaultBatchSize = 100
	// projectsPerRun bounds one run; the next run picks up the rest.
	projectsPerRun = 50
	// rowBatchFactor scales the batch for the cheap row-only statements
	// (parent pointers, upload markers, chunk rows).
	rowBatchFactor = 50
)

// Releaser gives a project's attributed bytes back to its owner's quota (the
// storage ledger). Deletion already released them; purging releases again so
// a project deleted before deletes released anything is not billed forever.
type Releaser interface {
	ReleaseProject(ctx context.Context, projectID snow.ID) (int64, error)
}

// ProjectStore deletes every chunk object of one project from the server's own
// store (storage.ProjectDeleter).
type ProjectStore interface {
	DeleteProject(ctx context.Context, projectID snow.ID) error
}

// Config tunes the purge.
type Config struct {
	// Retention is how long a deleted project stays restorable. Required.
	Retention time.Duration
	// BatchSize is the number of commits removed per transaction; zero uses
	// DefaultBatchSize.
	BatchSize int
}

// Stats describes one purged project.
type Stats struct {
	ProjectID snow.ID
	Commits   int64
	Trees     int64
	Chunks    int64
	Packs     int64
}

// Purger removes deleted projects past their retention. Every step runs in
// its own short transaction that first re-locks the project row and checks it
// is still deleted and past the cutoff, so a concurrent restore wins cleanly
// and two instances purging the same project serialize batch by batch.
type Purger struct {
	queries    *sqlcPostgres.Queries
	transactor *dbtx.Transactor
	audit      usecase.AuditRecorder
	releaser   Releaser
	store      ProjectStore
	retention  time.Duration
	batch      int64
	now        func() time.Time
}

// New builds a purger over the postgres database.
func New(db *sql.DB, cfg Config) (*Purger, error) {
	if cfg.Retention <= 0 {
		return nil, errors.New("projectpurge: retention must be positive")
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	return &Purger{
		queries:    sqlcPostgres.New(dbtx.New(db)),
		transactor: dbtx.NewTransactor(db),
		retention:  cfg.Retention,
		batch:      int64(batch),
		now:        time.Now,
	}, nil
}

// WithAudit records project.purged for every purged project.
func (p *Purger) WithAudit(rec usecase.AuditRecorder) *Purger {
	p.audit = rec
	return p
}

// WithReleaser releases the project's attributed storage in the final step.
func (p *Purger) WithReleaser(releaser Releaser) *Purger {
	p.releaser = releaser
	return p
}

// WithStore deletes the project's chunk objects from the server's own store
// once its chunk rows are gone. Without one the objects stay (the chunk GC
// does not adopt objects of a project that no longer exists).
func (p *Purger) WithStore(store ProjectStore) *Purger {
	p.store = store
	return p
}

// errGone stops a purge whose project was restored (or purged elsewhere).
var errGone = errors.New("project is no longer purgeable")

// PurgeExpired purges up to one run's worth of projects deleted before the
// retention cutoff and returns what it removed. A project that fails is
// logged and skipped so one bad project cannot block the others.
func (p *Purger) PurgeExpired(ctx context.Context) ([]Stats, error) {
	cutoff := p.now().Add(-p.retention)
	ids, err := p.queries.ListPurgeableProjects(ctx, sqlcPostgres.ListPurgeableProjectsParams{
		Cutoff: cutoff,
		Limit:  projectsPerRun,
	})
	if err != nil {
		return nil, fmt.Errorf("list purgeable projects: %w", err)
	}
	var purged []Stats
	for _, id := range ids {
		if ctx.Err() != nil {
			return purged, ctx.Err()
		}
		stats, err := p.PurgeProject(ctx, snow.ID(id), cutoff)
		switch {
		case errors.Is(err, errGone):
			continue
		case err != nil:
			slog.Error("project purge failed", "project_id", snow.ID(id).Base36(), "err", err)
			continue
		}
		purged = append(purged, stats)
	}
	return purged, nil
}

// PurgeProject removes one deleted project deleted before cutoff.
func (p *Purger) PurgeProject(ctx context.Context, projectID snow.ID, cutoff time.Time) (Stats, error) {
	stats := Stats{ProjectID: projectID}
	id := projectID.Int64()

	// Refs and merge requests first: they reference commits.
	if _, err := p.step(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
		for _, purge := range []func(context.Context, int64) (int64, error){
			p.queries.PurgeProjectFileLocks,
			p.queries.PurgeProjectMergeRequests,
			p.queries.PurgeProjectTags,
			p.queries.PurgeProjectBranches,
		} {
			if _, err := purge(ctx, id); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}); err != nil {
		return stats, err
	}

	// Commits reference their parents; detach them so any batch can go.
	if err := p.drain(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
		return p.queries.DetachProjectCommitParents(ctx, sqlcPostgres.DetachProjectCommitParentsParams{
			ProjectID: id,
			Limit:     p.batch * rowBatchFactor,
		})
	}); err != nil {
		return stats, err
	}

	// Commits with their trees: each commit owns its root tree, whose delete
	// cascades to its subtrees, files and file chunk links.
	if err := p.drain(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
		trees, err := p.queries.PurgeProjectCommitBatch(ctx, sqlcPostgres.PurgeProjectCommitBatchParams{
			ProjectID: id,
			Limit:     p.batch,
		})
		if err != nil || len(trees) == 0 {
			return 0, err
		}
		removed, err := p.queries.PurgeTreeRoots(ctx, trees)
		if err != nil {
			return 0, err
		}
		stats.Commits += int64(len(trees))
		stats.Trees += removed
		return int64(len(trees)), nil
	}); err != nil {
		return stats, err
	}

	if err := p.drain(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
		return p.queries.PurgeProjectChunkUploads(ctx, sqlcPostgres.PurgeProjectChunkUploadsParams{
			ProjectID: id,
			Limit:     p.batch * rowBatchFactor,
		})
	}); err != nil {
		return stats, err
	}
	// Chunk rows once no file references them any more (the trees above
	// took their file chunk links), then the pack rows no chunk row points
	// at any more, then the objects: rows first, so a failed object delete
	// leaves nothing pointing at missing content and the next run deletes the
	// prefixes again.
	if err := p.drain(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
		n, err := p.queries.PurgeProjectChunkRows(ctx, sqlcPostgres.PurgeProjectChunkRowsParams{
			ProjectID: id,
			Limit:     p.batch * rowBatchFactor,
		})
		stats.Chunks += n
		return n, err
	}); err != nil {
		return stats, err
	}
	if err := p.drain(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
		n, err := p.queries.PurgeProjectChunkPacks(ctx, sqlcPostgres.PurgeProjectChunkPacksParams{
			ProjectID: id,
			Limit:     p.batch * rowBatchFactor,
		})
		stats.Packs += n
		return n, err
	}); err != nil {
		return stats, err
	}
	if p.store != nil {
		if _, err := p.step(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
			external, err := p.queries.IsProjectOnOwnStorage(ctx, id)
			if err != nil || external {
				return 0, err
			}
			return 0, p.store.DeleteProject(ctx, projectID)
		}); err != nil {
			return stats, err
		}
	}
	// The release reads the project's usage counter, not its links, so it
	// still gives back what a never-released project held.
	if p.releaser != nil {
		if _, err := p.step(ctx, projectID, cutoff, func(ctx context.Context) (int64, error) {
			return p.releaser.ReleaseProject(ctx, projectID)
		}); err != nil {
			return stats, err
		}
	}

	// The project row last; the rest of its rows cascade, and its slug is
	// free again.
	err := p.transactor.WithinTx(ctx, func(ctx context.Context) error {
		project, err := p.lock(ctx, projectID, cutoff)
		if err != nil {
			return err
		}
		return usecase.AuditedWrite(ctx, p.audit, func(ctx context.Context) error {
			_, err := p.queries.PurgeProjectRow(ctx, id)
			return err
		}, func() *domain.AuditEntry {
			metadata := map[string]any{
				"name":    project.Name,
				"slug":    project.Slug,
				"commits": stats.Commits,
			}
			if project.DeletedAt.Valid {
				metadata["deleted_at"] = project.DeletedAt.Time.UTC().Format(time.RFC3339)
			}
			return &domain.AuditEntry{
				Action:     domain.AuditProjectPurged,
				OrgID:      domain.IDPtr(snow.ID(project.OrgID)),
				ProjectID:  domain.IDPtr(projectID),
				TargetType: domain.AuditTargetProject,
				TargetID:   projectID.Base36(),
				Metadata:   metadata,
			}
		})
	})
	if err != nil {
		return stats, err
	}
	slog.Info("project purged", "project_id", projectID.Base36(), "commits", stats.Commits, "trees", stats.Trees, "chunks", stats.Chunks, "packs", stats.Packs)
	return stats, nil
}

// drain repeats a batched step until it removes nothing.
func (p *Purger) drain(ctx context.Context, projectID snow.ID, cutoff time.Time, fn func(ctx context.Context) (int64, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := p.step(ctx, projectID, cutoff, fn)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// step runs fn in its own transaction after locking the project row.
func (p *Purger) step(ctx context.Context, projectID snow.ID, cutoff time.Time, fn func(ctx context.Context) (int64, error)) (int64, error) {
	var n int64
	err := p.transactor.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := p.lock(ctx, projectID, cutoff); err != nil {
			return err
		}
		var err error
		n, err = fn(ctx)
		return err
	})
	return n, err
}

func (p *Purger) lock(ctx context.Context, projectID snow.ID, cutoff time.Time) (sqlcPostgres.LockPurgeableProjectRow, error) {
	row, err := p.queries.LockPurgeableProject(ctx, sqlcPostgres.LockPurgeableProjectParams{
		ID:     projectID.Int64(),
		Cutoff: cutoff,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return row, errGone
	}
	return row, err
}
