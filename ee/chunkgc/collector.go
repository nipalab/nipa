// Package chunkgc deletes chunk content nothing references any more: no file
// of any commit, no project's upload marker and no storage ledger link.
// Chunks are content-addressed and shared across every project on the server,
// so a chunk only becomes garbage once the last reference is gone (a purged
// project, an upload rejected by the quota, an abandoned push).
//
// The collector marks, then sweeps:
//
//   - Mark: chunks without references get chunks.gc_marked_at.
//   - Adopt: chunk objects in the bucket without a chunks row (uploads that were
//     never confirmed) older than the grace period get a marked row, so they
//     go through the same sweep.
//   - Sweep: rows still unreferenced whose mark is older than the grace period
//     are locked (FOR UPDATE SKIP LOCKED), their objects deleted, then the rows.
//
// Every caller that is about to rely on chunk content clears its mark first
// (Guard.Protect, wired into usecase.Chunk). That update waits for a sweep
// holding the row, so content a sweep removed is reported missing and
// uploaded again; it is never linked while gone. Rows that did not exist when
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
	DeleteChunk(ctx context.Context, hash domain.Hash) error
	ListChunks(ctx context.Context, fn func(s3.StoredChunk) error) error
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
	slog.Info("chunk gc run finished", "mode", c.cfg.Mode,
		"marked", report.Marked, "adopted", report.Adopted,
		"deleted", report.Deleted, "deleted_bytes", report.DeletedBytes,
		"candidates", report.Candidates, "candidate_bytes", report.CandidateBytes)
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
		hashes := make([][]byte, 0, len(batch))
		for _, chunk := range batch {
			hashes = append(hashes, append([]byte(nil), chunk.Hash[:]...))
		}
		known, err := c.queries.ExistingChunkHashes(ctx, hashes)
		if err != nil {
			return fmt.Errorf("chunkgc: orphan lookup: %w", err)
		}
		recorded := make(map[domain.Hash]bool, len(known))
		for _, raw := range known {
			var hash domain.Hash
			copy(hash[:], raw)
			recorded[hash] = true
		}
		var orphanHashes [][]byte
		var orphanSizes []int64
		var bytes int64
		for _, chunk := range batch {
			if recorded[chunk.Hash] {
				continue
			}
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
				Hashes:   orphanHashes,
				Sizes:    orphanSizes,
				MarkedAt: now,
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
				if err := c.store.DeleteChunk(ctx, hash); err != nil {
					return fmt.Errorf("chunkgc: delete object %s: %w", hash, err)
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

// Guard clears garbage marks for content a caller is about to rely on; it
// implements usecase.ChunkGCGuard.
type Guard struct {
	queries *sqlcPostgres.Queries
}

// NewGuard builds the guard over the postgres database.
func NewGuard(db *sql.DB) *Guard {
	return &Guard{queries: sqlcPostgres.New(dbtx.New(db))}
}

// Protect clears the marks of hashes. It blocks while a sweep holds one of
// their rows; afterwards that row is gone and the content reads as missing.
func (g *Guard) Protect(ctx context.Context, hashes []domain.Hash) error {
	if len(hashes) == 0 {
		return nil
	}
	raw := make([][]byte, 0, len(hashes))
	for _, hash := range hashes {
		raw = append(raw, append([]byte(nil), hash[:]...))
	}
	if _, err := g.queries.UnmarkChunks(ctx, raw); err != nil {
		return domain.NewErrorDatabase(fmt.Sprintf("protect chunks from gc: %v", err))
	}
	return nil
}
