-- name: CommitGet :one
SELECT *
FROM commits
WHERE id = :id
LIMIT 1;

-- name: CommitGetByHash :one
SELECT *
FROM commits
WHERE hash = :hash
LIMIT 1;

-- name: TreeNodeGet :one
SELECT *
FROM tree_nodes
WHERE id = :id
LIMIT 1;

-- name: TreeNodeGetChildByName :one
SELECT *
FROM tree_nodes
WHERE parent_tree_id = :parent_tree_id AND name = :name
LIMIT 1;

-- name: TreeNodeListChildren :many
SELECT *
FROM tree_nodes
WHERE parent_tree_id = :parent_tree_id
ORDER BY name;

-- name: FileListByTree :many
SELECT *
FROM files
WHERE tree_id = :tree_id
ORDER BY name;

-- name: ChunkListByFile :many
SELECT chunks.id, chunks.hash, chunks.size_bytes, chunks.created_at
FROM chunks
JOIN file_chunks ON file_chunks.chunk_id = chunks.id
WHERE file_chunks.file_id = :file_id
ORDER BY file_chunks.chunk_index;
