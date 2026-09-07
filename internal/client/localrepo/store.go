package localrepo

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/nipalab/nipa/internal/domain"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS tree_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB NOT NULL UNIQUE,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    parent_tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB NOT NULL UNIQUE,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS file_chunks (
    file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_id INTEGER NOT NULL REFERENCES chunks(id),
    chunk_index INTEGER NOT NULL,
    PRIMARY KEY (file_id, chunk_index)
);

CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

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

	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := clearTree(tx); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "tree_hash", root.Hash.String()); err != nil {
		return err
	}

	var parent sql.NullInt64
	if err := l.insertTreeNode(tx, root, parent); err != nil {
		return err
	}
	return tx.Commit()
}

func clearTree(tx *sql.Tx) error {
	statements := []string{
		`DELETE FROM file_chunks`,
		`DELETE FROM files`,
		`DELETE FROM chunks`,
		`DELETE FROM tree_nodes`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalRepo) insertTreeNode(tx *sql.Tx, node *domain.TreeNode, parentID sql.NullInt64) error {
	res, err := tx.Exec(`INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (?, ?, ?, ?)`,
		node.Hash.Bytes(), node.Name, node.Mode, nullableInt64(parentID))
	if err != nil {
		return err
	}
	nodeID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	for _, file := range node.FileChildren {
		if err := l.insertFile(tx, nodeID, file); err != nil {
			return err
		}
	}
	for _, child := range node.TreeChildren {
		if err := l.insertTreeNode(tx, child, sql.NullInt64{Int64: nodeID, Valid: true}); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalRepo) insertFile(tx *sql.Tx, treeID int64, file *domain.File) error {
	res, err := tx.Exec(`INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary) VALUES (?, ?, ?, ?, ?, ?)`,
		file.Name, file.Mode, treeID, file.Hash.Bytes(), file.SizeBytes, file.IsBinary)
	if err != nil {
		return err
	}
	fileID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for index, chunk := range file.Chunks {
		chunkID, err := l.upsertChunk(tx, chunk.Hash)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (?, ?, ?)`,
			fileID, chunkID, index); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalRepo) upsertChunk(tx *sql.Tx, hash domain.Hash) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM chunks WHERE hash = ?`, hash.Bytes()).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO chunks (hash, size_bytes) VALUES (?, 0)`, hash.Bytes())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullableInt64(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}
