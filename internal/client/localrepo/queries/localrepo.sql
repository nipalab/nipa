-- name: MetaGet :one
SELECT value
FROM meta
WHERE key = :key
LIMIT 1;

-- name: MetaSet :exec
INSERT INTO meta (key, value)
VALUES (:key, :value)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: FileChunkClear :exec
DELETE FROM file_chunks;

-- name: ChunkClear :exec
DELETE FROM chunks;

-- name: FileClear :exec
DELETE FROM files;

-- name: TreeNodeClear :exec
DELETE FROM tree_nodes;

-- name: TreeInsert :one
INSERT INTO tree_nodes (hash, name, mode, parent_tree_id)
VALUES (:hash, :name, :mode, :parent_tree_id)
RETURNING id;

-- name: FileInsert :one
INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary)
VALUES (:name, :mode, :tree_id, :hash, :size_bytes, :is_binary)
RETURNING id;

-- name: ChunkUpsert :one
INSERT INTO chunks (hash, size_bytes)
VALUES (:hash, :size_bytes)
ON CONFLICT(hash) DO UPDATE SET hash = excluded.hash
RETURNING id;

-- name: FileChunkInsert :exec
INSERT INTO file_chunks (file_id, chunk_id, chunk_index)
VALUES (:file_id, :chunk_id, :chunk_index);