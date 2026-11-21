package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/ee/chunkgc"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/storage"
)

// The server's own bucket must support the collector.
var _ chunkgc.ObjectStore = (*s3store.Store)(nil)

// chunkGCSetup reads the collector settings. The grace period must cover a
// presign-to-confirm round (twice the presign TTL) so an orphan object a
// client is still claiming is never swept under it.
func chunkGCSetup(cfg *config.Config) (chunkgc.Mode, time.Duration, error) {
	mode, err := chunkgc.ParseMode(cfg.ChunkGCMode)
	if err != nil {
		return "", 0, err
	}
	grace := time.Duration(cfg.ChunkGCGraceHours) * time.Hour
	if grace <= 0 {
		grace = chunkgc.DefaultGrace
	}
	if ttl := time.Duration(cfg.ChunkPresignTTLSeconds) * time.Second; grace < 2*ttl {
		return "", 0, fmt.Errorf("CHUNK_GC_GRACE_HOURS (%s) must be at least twice CHUNK_PRESIGN_TTL_SECONDS (%s)", grace, ttl)
	}
	return mode, grace, nil
}

// newChunkGCGuard is the guard confirm and server-side uploads run; nil when
// the collector is off.
func newChunkGCGuard(mode chunkgc.Mode, dbConn *sql.DB) *chunkgc.Guard {
	if mode == chunkgc.ModeOff {
		return nil
	}
	return chunkgc.NewGuard(dbConn)
}

// newChunkGCJob builds the background collector over the server's own
// bucket; nil when the collector is off.
func newChunkGCJob(cfg *config.Config, mode chunkgc.Mode, grace time.Duration, dbConn *sql.DB, store storage.ChunkStore, metrics *obs.Metrics) (*chunkgc.Job, error) {
	if mode == chunkgc.ModeOff {
		slog.Info("chunk gc disabled")
		return nil, nil
	}
	objects, _ := store.(chunkgc.ObjectStore)
	collector, err := chunkgc.New(dbConn, objects, chunkgc.Config{
		Mode:       mode,
		Grace:      grace,
		OrphanScan: cfg.ChunkGCOrphanScan,
	})
	if err != nil {
		return nil, err
	}
	collector.WithMetrics(metrics)
	slog.Info("chunk gc enabled", "mode", mode, "grace", grace, "orphan_scan", cfg.ChunkGCOrphanScan)
	return chunkgc.NewJob(collector, chunkgc.DefaultInterval), nil
}
