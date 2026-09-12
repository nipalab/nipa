package localrepo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nipalab/nipa/internal/client/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

func (l *LocalRepo) SaveCommit(commitID, commitHash string) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}

	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcLocalrepo.New(tx)
	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: "commit_id", Value: commitID}); err != nil {
		return err
	}
	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: "commit_hash", Value: commitHash}); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *LocalRepo) LoadCommit() (*domain.LocalCommit, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}

	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)

	commitID, err := q.MetaGet(ctx, "commit_id")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	commitHash, err := q.MetaGet(ctx, "commit_hash")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return &domain.LocalCommit{CommitID: commitID, CommitHash: commitHash}, nil
}
