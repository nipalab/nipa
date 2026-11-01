-- name: ChunkInsertOrIgnore :exec
INSERT INTO chunks (hash, size_bytes) VALUES (?, ?) ON CONFLICT(hash) DO NOTHING;

-- name: ChunkGetByHash :one
SELECT *
FROM chunks
WHERE hash = :hash
LIMIT 1;

-- name: TreeNodeInsert :one
INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (?, ?, ?, ?)
RETURNING id;

-- name: TreeNodeSetParent :exec
UPDATE tree_nodes
SET parent_tree_id = :parent_tree_id
WHERE id = :id;

-- name: FileInsert :one
INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary, encoding) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: FileSetTree :exec
UPDATE files
SET tree_id = :tree_id
WHERE id = :id;

-- name: FileChunkInsert :exec
INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (?, ?, ?)
ON CONFLICT(file_id, chunk_index) DO UPDATE SET chunk_id = excluded.chunk_id;

-- name: CommitInsert :exec
INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, parent_2_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: BranchUpdateCommit :exec
UPDATE branches
SET commit_id = :commit_id, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND deleted = FALSE;
-- name: ProjectChunkUploadInsert :exec
INSERT INTO project_chunk_uploads (project_id, hash) VALUES (?, ?)
ON CONFLICT(project_id, hash) DO NOTHING;

-- name: ProjectChunkUploadList :many
SELECT hash FROM project_chunk_uploads
WHERE project_id = sqlc.arg(project_id) AND hash IN (sqlc.slice('hashes'));

-- name: ChunkGetByHashForProject :one
SELECT c.*
FROM chunks c
JOIN project_chunk_uploads u ON u.hash = c.hash
WHERE c.hash = sqlc.arg(hash) AND u.project_id = sqlc.arg(project_id)
LIMIT 1;
