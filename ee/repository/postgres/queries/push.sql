-- name: ChunkInsertOrIgnore :exec
INSERT INTO chunks (hash, size_bytes) VALUES (sqlc.arg(hash), sqlc.arg(size_bytes)) ON CONFLICT(hash) DO NOTHING;

-- name: ChunkGetByHash :one
SELECT *
FROM chunks
WHERE hash = sqlc.arg(hash)
LIMIT 1;

-- name: TreeNodeInsert :one
INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (sqlc.arg(hash), sqlc.arg(name), sqlc.arg(mode), sqlc.arg(parent_tree_id))
RETURNING id;

-- name: TreeNodeSetParent :exec
UPDATE tree_nodes
SET parent_tree_id = sqlc.arg(parent_tree_id)
WHERE id = sqlc.arg(id);

-- name: FileInsert :one
INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary, encoding) VALUES (sqlc.arg(name), sqlc.arg(mode), sqlc.arg(tree_id), sqlc.arg(hash), sqlc.arg(size_bytes), sqlc.arg(is_binary), sqlc.arg(encoding))
RETURNING id;

-- name: FileSetTree :exec
UPDATE files
SET tree_id = sqlc.arg(tree_id)
WHERE id = sqlc.arg(id);

-- name: FileChunkInsert :exec
INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (sqlc.arg(file_id), sqlc.arg(chunk_id), sqlc.arg(chunk_index))
ON CONFLICT(file_id, chunk_index) DO UPDATE SET chunk_id = excluded.chunk_id;

-- name: CommitInsert :exec
INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, parent_2_id, user_id, message) VALUES (sqlc.arg(id), sqlc.arg(hash), sqlc.arg(project_id), sqlc.arg(tree_id), sqlc.arg(parent_1_id), sqlc.arg(parent_2_id), sqlc.arg(user_id), sqlc.arg(message));

-- name: BranchUpdateCommit :exec
UPDATE branches
SET commit_id = sqlc.arg(commit_id), updated_at = now()
WHERE id = sqlc.arg(id) AND deleted = FALSE;

-- name: ProjectChunkUploadInsert :exec
INSERT INTO project_chunk_uploads (project_id, hash) VALUES (sqlc.arg(project_id), sqlc.arg(hash))
ON CONFLICT (project_id, hash) DO NOTHING;

-- name: ProjectChunkUploadList :many
SELECT hash FROM project_chunk_uploads
WHERE project_id = sqlc.arg(project_id) AND hash = ANY(sqlc.arg(hashes)::bytea[]);

-- name: ChunkGetByHashForProject :one
SELECT c.*
FROM chunks c
JOIN project_chunk_uploads u ON u.hash = c.hash
WHERE c.hash = sqlc.arg(hash) AND u.project_id = sqlc.arg(project_id)
LIMIT 1;
