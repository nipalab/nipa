// Package chunkgc deletes chunk content nothing references any more: no file
// of any commit of its project and no upload marker of its project. Chunks
// belong to one project (per-project dedup, keyed by (project, hash) in the
// database and p/<project>/... in the bucket), so every reference check stays
// inside the chunk's project. Garbage is content no push committed (an
// upload rejected by the quota, an abandoned push, an object whose confirm
// never ran); a purged project's rows and objects go with the purge.
//
// The collector marks, then sweeps:
//
//   - Mark: chunks without references get chunks.gc_marked_at.
//   - Adopt: chunk objects in the bucket without a chunks row for their
//     project (uploads that were never confirmed) older than the grace period
//     get a marked row, so they go through the same sweep. Objects of a
//     project that no longer exists are left to the purge.
//   - Sweep: rows still unreferenced whose mark is older than the grace period
//     are locked (FOR UPDATE SKIP LOCKED), their objects deleted, then the rows.
//     A packed row (chunks.pack_id) has no object of its own: its sweep
//     deletes only the row, and its bytes go with the pack.
//   - Packs: a pack no chunk row points at (a bundle whose confirm never ran,
//     or whose rows were all swept) or still pending (its write failed) is
//     marked (chunk_packs.gc_marked_at); a pack that gained rows again loses
//     its mark. Packs still unreferenced (or pending) whose mark is older than
//     the grace period are locked, their objects deleted, then the rows. A
//     pack can only gain rows through a receipt, and receipts expire after
//     the presign TTL (at most half the grace period), so a swept pack is
//     never referenced again. No bucket listing is needed for packs.
//
// Every caller that is about to rely on chunk content clears its mark first
// (Guard.Protect, wired into usecase.Chunk). That update waits for a sweep
// holding the row, so content a sweep removed is reported missing and
// uploaded again; it is never recorded while gone. Rows that did not exist when
// the caller checked (orphan objects) are protected by the grace period: an
// adopted row cannot be swept before it has been marked for that long.
//
// Own-storage (BYO) buckets are never touched: the sweep deletes from the
// server's own bucket only.
//
// Enterprise Edition: see ee/LICENSE.
package chunkgc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// Mode selects what the collector does.
type Mode string

const (
	// ModeOff runs no collector.
	ModeOff Mode = "off"
	// ModeDryRun marks garbage and reports what a sweep would delete, but
	// deletes and adopts nothing.
	ModeDryRun Mode = "dry-run"
	// ModeOn deletes garbage.
	ModeOn Mode = "on"
)

// ParseMode reads CHUNK_GC_MODE; empty is dry-run.
func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case "", ModeDryRun:
		return ModeDryRun, nil
	case ModeOn:
		return ModeOn, nil
	case ModeOff:
		return ModeOff, nil
	default:
		return "", fmt.Errorf("chunkgc: unknown mode %q (want off, dry-run or on)", raw)
	}
}

const (
	// DefaultGrace is how long a chunk stays marked before it is swept.
	DefaultGrace   = 24 * time.Hour
	markBatch      = 5000
	sweepBatch     = 100
	adoptBatch     = 1000
	advisoryLockID = int64(0x6e6970616763) // "nipagc"
)

// ObjectStore is the server's own chunk bucket.
type ObjectStore interface {
	DeleteChunk(ctx context.Context, projectID snow.ID, hash domain.Hash) error
	ListChunks(ctx context.Context, fn func(s3.StoredChunk) error) error
	DeletePack(ctx context.Context, key string) error
}

// Config tunes the collector.
type Config struct {
	Mode Mode
	// Grace is the minimum time a chunk stays marked (and an orphan object
	// exists) before it is deleted; zero uses DefaultGrace.
	Grace time.Duration
	// OrphanScan lists the bucket to adopt objects without a chunks row.
	OrphanScan bool
}

// Report is what one run did.
type Report struct {
	// Skipped is set when another instance held the collector lock.
	Skipped        bool
	Marked         int64
	MarkedBytes    int64
	Adopted        int64
	AdoptedBytes   int64
	Deleted        int64
	DeletedBytes   int64
	Candidates     int64
	CandidateBytes int64
	// Pack counterparts of the fields above.
	PacksMarked         int64
	PacksMarkedBytes    int64
	PacksDeleted        int64
	PacksDeletedBytes   int64
	PackCandidates      int64
	PackCandidatesBytes int64
}

// Collector runs the mark, adopt and sweep phases.
type Collector struct {
	db         *sql.DB
	queries    *sqlcPostgres.Queries
	transactor *dbtx.Transactor
	store      ObjectStore
	metrics    *obs.Metrics
	cfg        Config
	now        func() time.Time
}

// New builds a collector over the postgres database and the server's bucket.
func New(db *sql.DB, store ObjectStore, cfg Config) (*Collector, error) {
	if cfg.Mode != ModeDryRun && cfg.Mode != ModeOn {
		return nil, fmt.Errorf("chunkgc: mode %q runs no collector", cfg.Mode)
	}
	if store == nil {
		return nil, errors.New("chunkgc: the chunk store does not support deleting and listing chunks")
	}
	if cfg.Grace <= 0 {
		cfg.Grace = DefaultGrace
	}
	return &Collector{
		db:         db,
		queries:    sqlcPostgres.New(dbtx.New(db)),
		transactor: dbtx.NewTransactor(db),
		store:      store,
		cfg:        cfg,
		now:        time.Now,
	}, nil
}

// WithMetrics counts what every phase handled.
func (c *Collector) WithMetrics(metrics *obs.Metrics) *Collector {
	c.metrics = metrics
	return c
}

// Run performs one collection. Only one instance runs at a time: the others
// return a skipped report.
func (c *Collector) Run(ctx context.Context) (Report, error) {
	var report Report
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return report, fmt.Errorf("chunkgc: connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockID).Scan(&locked); err != nil {
		return report, fmt.Errorf("chunkgc: lock: %w", err)
	}
	if !locked {
		report.Skipped = true
		return report, nil
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, advisoryLockID)
	}()

	now := c.now()
	if err := c.mark(ctx, now, &report); err != nil {
		return report, err
	}
	if c.cfg.OrphanScan {
		if err := c.adopt(ctx, now, &report); err != nil {
			return report, err
		}
	}
	cutoff := now.Add(-c.cfg.Grace)
	if c.cfg.Mode == ModeDryRun {
		row, err := c.queries.CountSweepableChunks(ctx, cutoff)
		if err != nil {
			return report, fmt.Errorf("chunkgc: count: %w", err)
		}
		report.Candidates, report.CandidateBytes = row.Chunks, row.Bytes
		c.metrics.ObserveChunkGC("dry_run", row.Chunks, row.Bytes)
	} else if err := c.sweep(ctx, cutoff, &report); err != nil {
		return report, err
	}
	if err := c.collectPacks(ctx, now, cutoff, &report); err != nil {
		return report, err
	}
	slog.Info("chunk gc run finished", "mode", c.cfg.Mode,
		"marked", report.Marked, "adopted", report.Adopted,
		"deleted", report.Deleted, "deleted_bytes", report.DeletedBytes,
		"candidates", report.Candidates, "candidate_bytes", report.CandidateBytes,
		"packs_marked", report.PacksMarked, "packs_deleted", report.PacksDeleted,
		"packs_deleted_bytes", report.PacksDeletedBytes, "pack_candidates", report.PackCandidates)
	return report, nil
}

// mark stamps unreferenced chunks, walking the id space in ranges so no
// statement scans the whole table.
func (c *Collector) mark(ctx context.Context, now time.Time, report *Report) error {
	maxID, err := c.queries.MaxChunkID(ctx)
	if err != nil {
		return fmt.Errorf("chunkgc: max chunk id: %w", err)
	}
	for after := int64(0); after < maxID; after += markBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		sizes, err := c.queries.MarkUnreferencedChunks(ctx, sqlcPostgres.MarkUnreferencedChunksParams{
			MarkedAt: now,
			AfterID:  after,
			UptoID:   after + markBatch,
		})
		if err != nil {
			return fmt.Errorf("chunkgc: mark: %w", err)
		}
		var bytes int64
		for _, size := range sizes {
			bytes += size
		}
		report.Marked += int64(len(sizes))
		report.MarkedBytes += bytes
		c.metrics.ObserveChunkGC("marked", int64(len(sizes)), bytes)
	}
	return nil
}

// adopt gives chunk objects older than the grace period that have no chunks
// row a marked row. In dry-run mode it only counts them.
func (c *Collector) adopt(ctx context.Context, now time.Time, report *Report) error {
	cutoff := now.Add(-c.cfg.Grace)
	var batch []s3.StoredChunk
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		defer func() { batch = batch[:0] }()
		projects := make([]int64, 0, len(batch))
		hashes := make([][]byte, 0, len(batch))
		for _, chunk := range batch {
			projects = append(projects, chunk.ProjectID.Int64())
			hashes = append(hashes, append([]byte(nil), chunk.Hash[:]...))
		}
		known, err := c.queries.ExistingProjectChunks(ctx, sqlcPostgres.ExistingProjectChunksParams{
			ProjectIds: projects,
			Hashes:     hashes,
		})
		if err != nil {
			return fmt.Errorf("chunkgc: orphan lookup: %w", err)
		}
		recorded := make(map[projectChunk]bool, len(known))
		for _, row := range known {
			key := projectChunk{project: snow.ID(row.ProjectID)}
			copy(key.hash[:], row.Hash)
			recorded[key] = true
		}
		var orphanProjects []int64
		var orphanHashes [][]byte
		var orphanSizes []int64
		var bytes int64
		for _, chunk := range batch {
			if recorded[projectChunk{project: chunk.ProjectID, hash: chunk.Hash}] {
				continue
			}
			orphanProjects = append(orphanProjects, chunk.ProjectID.Int64())
			orphanHashes = append(orphanHashes, append([]byte(nil), chunk.Hash[:]...))
			orphanSizes = append(orphanSizes, chunk.SizeBytes)
			bytes += chunk.SizeBytes
		}
		if len(orphanHashes) == 0 {
			return nil
		}
		count := int64(len(orphanHashes))
		if c.cfg.Mode == ModeOn {
			count, err = c.queries.AdoptOrphanChunks(ctx, sqlcPostgres.AdoptOrphanChunksParams{
				ProjectIds: orphanProjects,
				Hashes:     orphanHashes,
				Sizes:      orphanSizes,
				MarkedAt:   now,
			})
			if err != nil {
				return fmt.Errorf("chunkgc: adopt: %w", err)
			}
		}
		report.Adopted += count
		report.AdoptedBytes += bytes
		c.metrics.ObserveChunkGC("adopted", count, bytes)
		return nil
	}
	err := c.store.ListChunks(ctx, func(chunk s3.StoredChunk) error {
		if !chunk.LastModified.Before(cutoff) {
			return nil
		}
		batch = append(batch, chunk)
		if len(batch) >= adoptBatch {
			return flush()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("chunkgc: orphan scan: %w", err)
	}
	return flush()
}

// sweep deletes marked chunks past the cutoff, one locked batch per
// transaction: objects first, then rows. A failed object delete rolls the
// batch back; the rows stay marked and the next run retries.
func (c *Collector) sweep(ctx context.Context, cutoff time.Time, report *Report) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var deleted, bytes int64
		err := c.transactor.WithinTx(ctx, func(ctx context.Context) error {
			rows, err := c.queries.LockSweepableChunks(ctx, sqlcPostgres.LockSweepableChunksParams{
				Cutoff: cutoff,
				Limit:  sweepBatch,
			})
			if err != nil {
				return fmt.Errorf("chunkgc: lock: %w", err)
			}
			ids := make([]int64, 0, len(rows))
			for _, row := range rows {
				var hash domain.Hash
				copy(hash[:], row.Hash)
				// A packed row's bytes belong to its pack, which the pack
				// phase deletes once no row points at it.
				if !row.PackID.Valid {
					if err := c.store.DeleteChunk(ctx, snow.ID(row.ProjectID), hash); err != nil {
						return fmt.Errorf("chunkgc: delete object %s: %w", hash, err)
					}
				}
				ids = append(ids, row.ID)
				bytes += row.SizeBytes
			}
			if len(ids) == 0 {
				return nil
			}
			deleted, err = c.queries.DeleteChunksByIDs(ctx, ids)
			if err != nil {
				return fmt.Errorf("chunkgc: delete rows: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return nil
		}
		report.Deleted += deleted
		report.DeletedBytes += bytes
		c.metrics.ObserveChunkGC("deleted", deleted, bytes)
	}
}

// collectPacks marks unreferenced and pending packs, clears the marks of
// packs that gained rows, and sweeps (or, in dry-run mode, counts) the packs
// marked before cutoff.
func (c *Collector) collectPacks(ctx context.Context, now, cutoff time.Time, report *Report) error {
	if _, err := c.queries.UnmarkReferencedChunkPacks(ctx); err != nil {
		return fmt.Errorf("chunkgc: unmark packs: %w", err)
	}
	sizes, err := c.queries.MarkChunkPacks(ctx, now)
	if err != nil {
		return fmt.Errorf("chunkgc: mark packs: %w", err)
	}
	var marked int64
	for _, size := range sizes {
		marked += size
	}
	report.PacksMarked += int64(len(sizes))
	report.PacksMarkedBytes += marked
	c.metrics.ObserveChunkGC("packs_marked", int64(len(sizes)), marked)
	if c.cfg.Mode == ModeDryRun {
		row, err := c.queries.CountSweepableChunkPacks(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("chunkgc: count packs: %w", err)
		}
		report.PackCandidates, report.PackCandidatesBytes = row.Packs, row.Bytes
		c.metrics.ObserveChunkGC("packs_dry_run", row.Packs, row.Bytes)
		return nil
	}
	return c.sweepPacks(ctx, cutoff, report)
}

// sweepPacks deletes marked packs past the cutoff that are still
// unreferenced (or pending), one locked batch per transaction: objects
// first, then rows. A failed object delete rolls the batch back and the next
// run retries.
func (c *Collector) sweepPacks(ctx context.Context, cutoff time.Time, report *Report) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var deleted, bytes int64
		err := c.transactor.WithinTx(ctx, func(ctx context.Context) error {
			rows, err := c.queries.LockSweepableChunkPacks(ctx, sqlcPostgres.LockSweepableChunkPacksParams{
				Cutoff: cutoff,
				Limit:  sweepBatch,
			})
			if err != nil {
				return fmt.Errorf("chunkgc: lock packs: %w", err)
			}
			ids := make([]int64, 0, len(rows))
			for _, row := range rows {
				key := storage.PackKey(snow.ID(row.ProjectID), snow.ID(row.ID))
				if err := c.store.DeletePack(ctx, key); err != nil {
					return fmt.Errorf("chunkgc: delete pack %s: %w", key, err)
				}
				ids = append(ids, row.ID)
				bytes += row.SizeBytes
			}
			if len(ids) == 0 {
				return nil
			}
			deleted, err = c.queries.DeleteChunkPacksByIDs(ctx, ids)
			if err != nil {
				return fmt.Errorf("chunkgc: delete pack rows: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return nil
		}
		report.PacksDeleted += deleted
		report.PacksDeletedBytes += bytes
		c.metrics.ObserveChunkGC("packs_deleted", deleted, bytes)
	}
}

// projectChunk identifies one chunk row: chunks are unique per project.
type projectChunk struct {
	project snow.ID
	hash    domain.Hash
}

// Guard clears garbage marks for content a caller is about to rely on; it
// implements usecase.ChunkGCGuard.
type Guard struct {
	queries *sqlcPostgres.Queries
}

// NewGuard builds the guard over the postgres database.
func NewGuard(db *sql.DB) *Guard {
	return &Guard{queries: sqlcPostgres.New(dbtx.New(db))}
}

// Protect clears the marks of the project's chunks with these hashes. It
// blocks while a sweep holds one of their rows; afterwards that row is gone
// and the content reads as missing.
func (g *Guard) Protect(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error {
	if len(hashes) == 0 {
		return nil
	}
	raw := make([][]byte, 0, len(hashes))
	for _, hash := range hashes {
		raw = append(raw, append([]byte(nil), hash[:]...))
	}
	if _, err := g.queries.UnmarkChunks(ctx, sqlcPostgres.UnmarkChunksParams{ProjectID: projectID.Int64(), Hashes: raw}); err != nil {
		return domain.NewErrorDatabase(fmt.Sprintf("protect chunks from gc: %v", err))
	}
	return nil
}
