package localrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// syncedTreeKey is the meta row that says which commit's tree the stored rows
// hold and which sparse set that tree was fetched with.
const syncedTreeKey = "synced_tree"

type syncedTreeRecord struct {
	Commit string   `json:"commit"`
	Sparse []string `json:"sparse"`
}

// canonicalSparse is the stable form of a sparse set: sorted, without
// duplicates or empty entries, and an empty (never nil) list for a full
// checkout.
func canonicalSparse(sparse []string) []string {
	out := make([]string, 0, len(sparse))
	for _, p := range sparse {
		if p != "" {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SaveSyncedTree is SaveTree for a tree that is commitID's tree fetched with
// the sparse set: the rows and the synced-tree record land in one
// transaction. An empty commitID records nothing, like SaveTree.
func (l *LocalRepo) SaveSyncedTree(root *domain.TreeNode, commitID string, sparse []string) error {
	if commitID == "" {
		return l.SaveTree(root)
	}
	value, err := json.Marshal(syncedTreeRecord{Commit: commitID, Sparse: canonicalSparse(sparse)})
	if err != nil {
		return err
	}
	return l.saveTree(root, func(ctx context.Context, q *sqlcLocalrepo.Queries) error {
		return q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: syncedTreeKey, Value: string(value)})
	})
}

// ClearSyncedTree drops the synced-tree record, leaving the stored tree rows
// alone. A sync calls it before it writes to the working copy: until the sync
// saves its tree, the working copy no longer matches what the record says, so
// an update interrupted or failed mid-write must not let the next one exit
// early.
func (l *LocalRepo) ClearSyncedTree() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	return sqlcLocalrepo.New(l.db).MetaDelete(context.Background(), syncedTreeKey)
}

// LoadSyncedTree returns the synced-tree record, or nil when the stored tree
// was not saved by SaveSyncedTree (or the record is unreadable): callers must
// then assume nothing about what the stored tree holds.
func (l *LocalRepo) LoadSyncedTree() (*clientDomain.SyncedTree, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	value, err := sqlcLocalrepo.New(l.db).MetaGet(context.Background(), syncedTreeKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record syncedTreeRecord
	if err := json.Unmarshal([]byte(value), &record); err != nil || record.Commit == "" {
		return nil, nil
	}
	return &clientDomain.SyncedTree{CommitID: record.Commit, Sparse: canonicalSparse(record.Sparse)}, nil
}
