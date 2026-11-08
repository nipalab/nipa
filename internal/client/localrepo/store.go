package localrepo

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "embed"

	_ "modernc.org/sqlite"

	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

//go:embed schema/schema.sql
var schemaSQL string

type LocalRepo struct {
	target string
	db     *sql.DB
}

func NewLocalRepo() *LocalRepo {
	return &LocalRepo{}
}

func NewLocalRepoWithTarget(target string) *LocalRepo {
	return &LocalRepo{target: target}
}

func (l *LocalRepo) Init(target string) error {
	if l.db != nil {
		_ = l.db.Close()
		l.db = nil
	}
	dir := filepath.Join(target, ConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, ObjectsDir), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, DBFile)+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return err
	}
	l.target = target
	l.db = db
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		l.db = nil
		return err
	}
	return nil
}

func (l *LocalRepo) Close() error {
	if l.db == nil {
		return nil
	}
	err := l.db.Close()
	l.db = nil
	return err
}

func (l *LocalRepo) SaveTree(root *domain.TreeNode) error {
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

	token := ""
	if root != nil {
		token = root.Hash.String()
	}

	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{
		Key:   "tree_hash",
		Value: token,
	}); err != nil {
		return err
	}

	nodes, files := flattenTree(root)
	if err := l.syncRows(ctx, q, nodes, files); err != nil {
		return err
	}
	// Stat rows follow the tracked set: paths gone from the snapshot are no
	// longer consulted by status, so drop them in the same transaction.
	if err := q.StatCacheSweep(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func flattenTree(root *domain.TreeNode) ([]sqlcLocalrepo.TreeNodeUpsertParams, []sqlcLocalrepo.FileUpsertParams) {
	var (
		nodes []sqlcLocalrepo.TreeNodeUpsertParams
		files []sqlcLocalrepo.FileUpsertParams
	)
	var walk func(node *domain.TreeNode, parentPath, path string)
	walk = func(node *domain.TreeNode, parentPath, path string) {
		nodes = append(nodes, sqlcLocalrepo.TreeNodeUpsertParams{
			Path:       path,
			ParentPath: parentPath,
			Hash:       node.Hash.Bytes(),
			Mode:       int64(node.Mode),
		})
		for _, file := range node.FileChildren {
			hashes := make([]domain.Hash, 0, len(file.Chunks))
			for _, c := range file.Chunks {
				hashes = append(hashes, c.Hash)
			}
			files = append(files, sqlcLocalrepo.FileUpsertParams{
				Path:      path + "/" + file.Name,
				TreePath:  path,
				Hash:      file.Hash.Bytes(),
				SizeBytes: file.SizeBytes,
				Mode:      int64(file.Mode),
				IsBinary:  file.IsBinary,
				Encoding:  file.Encoding,
				Chunks:    encodeChunkHashes(hashes),
			})
		}
		for _, child := range node.TreeChildren {
			walk(child, path, path+"/"+child.Name)
		}
	}
	if root != nil {
		walk(root, "", "")
	}
	return nodes, files
}

// syncRows makes the stored tree equal to the wanted one while writing only the
// rows that differ: a save after a small change costs a read of the stored rows
// and a handful of writes, not a rewrite of the whole tree. Nodes are listed
// parent-first, so upserting them in order satisfies the files' foreign key.
func (l *LocalRepo) syncRows(ctx context.Context, q *sqlcLocalrepo.Queries, nodes []sqlcLocalrepo.TreeNodeUpsertParams, files []sqlcLocalrepo.FileUpsertParams) error {
	storedNodes, err := q.TreeNodeList(ctx)
	if err != nil {
		return err
	}
	storedFiles, err := q.FileList(ctx)
	if err != nil {
		return err
	}

	nodeByPath := make(map[string]sqlcLocalrepo.TreeNode, len(storedNodes))
	for _, n := range storedNodes {
		nodeByPath[n.Path] = n
	}
	fileByPath := make(map[string]sqlcLocalrepo.File, len(storedFiles))
	for _, f := range storedFiles {
		fileByPath[f.Path] = f
	}

	for _, want := range nodes {
		stored, ok := nodeByPath[want.Path]
		delete(nodeByPath, want.Path)
		if ok && sameNode(stored, want) {
			continue
		}
		if err := q.TreeNodeUpsert(ctx, want); err != nil {
			return err
		}
	}
	for _, want := range files {
		stored, ok := fileByPath[want.Path]
		delete(fileByPath, want.Path)
		if ok && sameFile(stored, want) {
			continue
		}
		if err := q.FileUpsert(ctx, want); err != nil {
			return err
		}
	}

	for path := range fileByPath {
		if err := q.FileDelete(ctx, path); err != nil {
			return err
		}
	}
	for path := range nodeByPath {
		if err := q.TreeNodeDelete(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

func sameNode(stored sqlcLocalrepo.TreeNode, want sqlcLocalrepo.TreeNodeUpsertParams) bool {
	return stored.ParentPath == want.ParentPath &&
		stored.Mode == want.Mode &&
		bytes.Equal(stored.Hash, want.Hash)
}

func sameFile(stored sqlcLocalrepo.File, want sqlcLocalrepo.FileUpsertParams) bool {
	return stored.TreePath == want.TreePath &&
		stored.SizeBytes == want.SizeBytes &&
		stored.Mode == want.Mode &&
		stored.IsBinary == want.IsBinary &&
		stored.Encoding == want.Encoding &&
		bytes.Equal(stored.Hash, want.Hash) &&
		bytes.Equal(stored.Chunks, want.Chunks)
}
