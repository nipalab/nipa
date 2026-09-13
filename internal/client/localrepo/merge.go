package localrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/nipalab/nipa/internal/client/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

const mergeStateKey = "merge_state"

// SaveMergeState persists the pending-merge metadata (source head + base tree)
// so the follow-up push attaches the second parent.
func (l *LocalRepo) SaveMergeState(state *domain.MergeState) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	q := sqlcLocalrepo.New(l.db)
	return q.MetaSet(context.Background(), sqlcLocalrepo.MetaSetParams{Key: mergeStateKey, Value: string(data)})
}

// LoadMergeState returns the pending-merge state, or nil when no merge is in
// progress.
func (l *LocalRepo) LoadMergeState() (*domain.MergeState, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	q := sqlcLocalrepo.New(l.db)
	value, err := q.MetaGet(context.Background(), mergeStateKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var state domain.MergeState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// ClearMergeState removes any pending-merge state after the merge completes.
func (l *LocalRepo) ClearMergeState() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	q := sqlcLocalrepo.New(l.db)
	return q.MetaDelete(context.Background(), mergeStateKey)
}
