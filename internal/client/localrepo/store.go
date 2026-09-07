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

func (l *LocalRepo) Init(target string) error {
	dir := filepath.Join(target, ConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, DBFile))
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
	if err := clearTree(ctx, q); err != nil {
		return err
	}

	treeHash := ""
	if root != nil {
		treeHash = root.Hash.String()
	}
	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{
		Key:   "tree_hash",
		Value: treeHash,
	}); err != nil {
		return err
	}

	if root == nil {
		return tx.Commit()
	}

	if err := l.insertTreeNode(ctx, q, root, sql.NullInt64{}); err != nil {
		return err
	}
	return tx.Commit()
}

func clearTree(ctx context.Context, q *sqlcLocalrepo.Queries) error {
	if err := q.FileChunkClear(ctx); err != nil {
		return err
	}
	if err := q.ChunkClear(ctx); err != nil {
		return err
	}
	if err := q.FileClear(ctx); err != nil {
		return err
	}
	return q.TreeNodeClear(ctx)
}

func (l *LocalRepo) insertTreeNode(ctx context.Context, q *sqlcLocalrepo.Queries, node *domain.TreeNode, parentID sql.NullInt64) error {
	nodeID, err := q.TreeInsert(ctx, sqlcLocalrepo.TreeInsertParams{
		Hash:         node.Hash.Bytes(),
		Name:         node.Name,
		Mode:         int64(node.Mode),
		ParentTreeID: parentID,
	})
	if err != nil {
		return err
	}

	for _, file := range node.FileChildren {
		if err := l.insertFile(ctx, q, nodeID, file); err != nil {
			return err
		}
	}
	for _, child := range node.TreeChildren {
		if err := l.insertTreeNode(ctx, q, child, sql.NullInt64{Int64: nodeID, Valid: true}); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalRepo) insertFile(ctx context.Context, q *sqlcLocalrepo.Queries, treeID int64, file *domain.File) error {
	fileID, err := q.FileInsert(ctx, sqlcLocalrepo.FileInsertParams{
		Name:      file.Name,
		Mode:      int64(file.Mode),
		TreeID:    sql.NullInt64{Int64: treeID, Valid: true},
		Hash:      file.Hash.Bytes(),
		SizeBytes: file.SizeBytes,
		IsBinary:  file.IsBinary,
	})
	if err != nil {
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
			FileID:     fileID,
			ChunkID:    chunkID,
			ChunkIndex: int64(index),
		}); err != nil {
			return err
		}
	}
	return nil
}
