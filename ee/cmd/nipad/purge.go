package main

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/ee/projectpurge"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/usecase"
)

// purgeRetention is how long a deleted project stays restorable; zero (purge
// off) keeps deleted projects restorable.
func purgeRetention(cfg *config.Config) time.Duration {
	if cfg.StoragePurgeRetentionDays <= 0 {
		return 0
	}
	return time.Duration(cfg.StoragePurgeRetentionDays) * 24 * time.Hour
}

// newPurgeJob builds the background purge of deleted projects past their
// retention; nil when STORAGE_PURGE_RETENTION_DAYS is 0.
func newPurgeJob(cfg *config.Config, dbConn *sql.DB, ledger *storageusage.Ledger, audit usecase.AuditRecorder) (*projectpurge.Job, error) {
	retention := purgeRetention(cfg)
	if retention == 0 {
		slog.Info("project purge disabled; deleted projects stay restorable")
		return nil, nil
	}
	purger, err := projectpurge.New(dbConn, projectpurge.Config{
		Retention: retention,
		BatchSize: cfg.StoragePurgeBatchSize,
	})
	if err != nil {
		return nil, err
	}
	purger.WithAudit(audit)
	if ledger != nil {
		purger.WithReleaser(ledger)
	}
	slog.Info("project purge enabled", "retention_days", cfg.StoragePurgeRetentionDays)
	return projectpurge.NewJob(purger, projectpurge.DefaultInterval), nil
}
