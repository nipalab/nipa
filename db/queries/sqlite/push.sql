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

-- name: FileSetTree :exec
UPDATE files
SET tree_id = :tree_id
WHERE id = :id;

-- name: CommitInsert :exec
INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, parent_2_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: BranchUpdateCommit :exec
UPDATE branches
SET commit_id = :commit_id, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND deleted = FALSE;
-- name: ProjectChunkUploadInsert :exec
INSERT INTO project_chunk_uploads (project_id, hash) VALUES (?, ?)
ON CONFLICT(project_id, hash) DO NOTHING;

-- name: ProjectChunkLinkedSizes :many
SELECT c.hash, c.size_bytes
FROM chunks c
JOIN project_chunk_uploads u ON u.hash = c.hash
WHERE u.project_id = sqlc.arg(project_id) AND c.hash IN (sqlc.slice('hashes'));

-- name: ChunkListByHashesForProject :many
SELECT c.id, c.hash
FROM chunks c
JOIN project_chunk_uploads u ON u.hash = c.hash
WHERE u.project_id = sqlc.arg(project_id) AND c.hash IN (sqlc.slice('hashes'));

-- name: FileInsertMany :many
INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary, encoding)
SELECT json_extract(value, '$.name'), json_extract(value, '$.mode'), json_extract(value, '$.tree_id'),
       unhex(json_extract(value, '$.hash')), json_extract(value, '$.size_bytes'),
       json_extract(value, '$.is_binary'), json_extract(value, '$.encoding')
FROM json_each(sqlc.arg(file_rows))
WHERE true
RETURNING id, tree_id, name;

-- name: FileChunkInsertMany :exec
INSERT INTO file_chunks (file_id, chunk_id, chunk_index)
SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]')
FROM json_each(sqlc.arg(links))
WHERE true
ON CONFLICT(file_id, chunk_index) DO UPDATE SET chunk_id = excluded.chunk_id;
