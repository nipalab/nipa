package localrepo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nipalab/nipa/internal/client/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

func (l *LocalRepo) LoadStatCache() (map[string]domain.StatEntry, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	rows, err := q.StatCacheList(ctx)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]domain.StatEntry, len(rows))
	for _, r := range rows {
		entries[r.Path] = domain.StatEntry{
			SizeBytes: r.SizeBytes,
			MtimeNS:   r.MtimeNs,
			Mode:      int(r.Mode),
			Hash:      hashFromBytes(r.Hash),
			CachedAt:  r.CachedAt,
		}
	}
	return entries, nil
}

// StatEntries returns the fingerprints of the given working-copy relative
// paths, one indexed read each, for callers that touch a few files of a large
// clone. Paths without a fingerprint are absent from the result.
func (l *LocalRepo) StatEntries(paths []string) (map[string]domain.StatEntry, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	entries := make(map[string]domain.StatEntry, len(paths))
	for _, p := range paths {
		r, err := q.StatCacheGet(ctx, p)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries[r.Path] = domain.StatEntry{
			SizeBytes: r.SizeBytes,
			MtimeNS:   r.MtimeNs,
			Mode:      int(r.Mode),
			Hash:      hashFromBytes(r.Hash),
			CachedAt:  r.CachedAt,
		}
	}
	return entries, nil
}

func (l *LocalRepo) SaveStatEntries(entries map[string]domain.StatEntry) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	if len(entries) == 0 {
		return nil
	}
	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcLocalrepo.New(tx)
	for path, entry := range entries {
		if err := q.StatCacheUpsert(ctx, sqlcLocalrepo.StatCacheUpsertParams{
			Path:      path,
			SizeBytes: entry.SizeBytes,
			MtimeNs:   entry.MtimeNS,
			Mode:      int64(entry.Mode),
			Hash:      entry.Hash.Bytes(),
			CachedAt:  entry.CachedAt,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *LocalRepo) SweepStatCache() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	return q.StatCacheSweep(ctx)
}
