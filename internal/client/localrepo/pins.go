package localrepo

import (
	"context"
	"errors"
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// Pins are stored like snapshot paths: slash separated, without a leading
// slash (serverDomain.NormalizePathPrefix). The repository root cannot be
// pinned.
func normalizePins(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		n, err := serverDomain.NormalizePathPrefix(p)
		if err != nil {
			return nil, err
		}
		if n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

func (l *LocalRepo) PinPaths(paths []string) error {
	return l.editPins(paths, func(q *sqlcLocalrepo.Queries, p string) error {
		return q.PinnedPathUpsert(context.Background(), sqlcLocalrepo.PinnedPathUpsertParams{Path: p, PinnedAt: time.Now().UnixNano()})
	})
}

func (l *LocalRepo) UnpinPaths(paths []string) error {
	return l.editPins(paths, func(q *sqlcLocalrepo.Queries, p string) error {
		return q.PinnedPathDelete(context.Background(), p)
	})
}

func (l *LocalRepo) ListPins() ([]string, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	return sqlcLocalrepo.New(l.db).PinnedPathList(context.Background())
}

func (l *LocalRepo) editPins(paths []string, apply func(*sqlcLocalrepo.Queries, string) error) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	normalized, err := normalizePins(paths)
	if err != nil || len(normalized) == 0 {
		return err
	}
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := sqlcLocalrepo.New(tx)
	for _, p := range normalized {
		if err := apply(q, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}
