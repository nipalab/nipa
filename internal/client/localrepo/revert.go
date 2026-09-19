package localrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/nipalab/nipa/internal/client/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

const revertStateKey = "revert_state"

func (l *LocalRepo) SaveRevertState(state *domain.RevertState) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	q := sqlcLocalrepo.New(l.db)
	return q.MetaSet(context.Background(), sqlcLocalrepo.MetaSetParams{Key: revertStateKey, Value: string(data)})
}

func (l *LocalRepo) LoadRevertState() (*domain.RevertState, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	q := sqlcLocalrepo.New(l.db)
	value, err := q.MetaGet(context.Background(), revertStateKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var state domain.RevertState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (l *LocalRepo) ClearRevertState() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	q := sqlcLocalrepo.New(l.db)
	return q.MetaDelete(context.Background(), revertStateKey)
}
