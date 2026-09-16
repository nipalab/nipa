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

-- name: CommitLog :many
WITH RECURSIVE commit_chain(id, depth) AS (
    SELECT :start_commit_id AS id, 0 AS depth
    UNION ALL
    SELECT c.parent_1_id, cc.depth + 1 FROM commits c JOIN commit_chain cc ON c.id = cc.id
    WHERE c.parent_1_id IS NOT NULL
)
SELECT c.id, c.hash, c.project_id, c.tree_id, c.parent_1_id, c.parent_2_id, c.user_id, c.message, c.created_at,
       u.name AS author_name, u.email AS author_email
FROM commits c
JOIN users u ON c.user_id = u.id
JOIN commit_chain cc ON c.id = cc.id
WHERE c.project_id = :project_id
ORDER BY cc.depth ASC, c.id DESC
LIMIT :limit;

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
