package localrepo

import (
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
	dir := filepath.Join(target, ConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, DBFile)+"?_pragma=foreign_keys(ON)")
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

	if root != nil {
		if err := l.insertTreeNode(ctx, q, root, "", "", token); err != nil {
			return err
		}
	}

	// Mark-and-sweep: only rows not stamped with the current snapshot are
	// stale (files removed or renamed since the last save), so the DELETE
	// here is bounded by the size of the change, not the whole repo.
	if err := q.StaleFileDelete(ctx, token); err != nil {
		return err
	}
	if err := q.StaleTreeNodeDelete(ctx, token); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *LocalRepo) insertTreeNode(ctx context.Context, q *sqlcLocalrepo.Queries, node *domain.TreeNode, parentPath, path, token string) error {
	if err := q.TreeNodeUpsert(ctx, sqlcLocalrepo.TreeNodeUpsertParams{
		Path:       path,
		ParentPath: parentPath,
		Hash:       node.Hash.Bytes(),
		Mode:       int64(node.Mode),
		SnapshotID: token,
	}); err != nil {
		return err
	}

	for _, file := range node.FileChildren {
		if err := l.insertFile(ctx, q, path, token, file); err != nil {
			return err
		}
	}
	for _, child := range node.TreeChildren {
		childPath := path + "/" + child.Name
		if err := l.insertTreeNode(ctx, q, child, path, childPath, token); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalRepo) insertFile(ctx context.Context, q *sqlcLocalrepo.Queries, treePath, token string, file *domain.File) error {
	filePath := treePath + "/" + file.Name
	if err := q.FileUpsert(ctx, sqlcLocalrepo.FileUpsertParams{
		Path:       filePath,
		TreePath:   treePath,
		Hash:       file.Hash.Bytes(),
		SizeBytes:  file.SizeBytes,
		Mode:       int64(file.Mode),
		IsBinary:   file.IsBinary,
		SnapshotID: token,
	}); err != nil {
		return err
	}
	for index, chunk := range file.Chunks {
		chunkID, err := q.ChunkUpsert(ctx, sqlcLocalrepo.ChunkUpsertParams{
			Hash:      chunk.Hash.Bytes(),
			SizeBytes: chunk.SizeBytes,
		})
		if err != nil {
			return err
		}
		if err := q.FileChunkInsert(ctx, sqlcLocalrepo.FileChunkInsertParams{
			FilePath:   filePath,
			ChunkID:    chunkID,
			ChunkIndex: int64(index),
		}); err != nil {
			return err
		}
	}
	return nil
}
