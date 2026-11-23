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
INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, parent_2_id, user_id, message, author_name, author_email, origin, created_at)
VALUES (sqlc.arg(id), sqlc.arg(hash), sqlc.arg(project_id), sqlc.arg(tree_id), sqlc.narg(parent_1_id), sqlc.narg(parent_2_id), sqlc.arg(user_id), sqlc.arg(message),
        sqlc.narg(author_name), sqlc.narg(author_email), sqlc.narg(origin), COALESCE(sqlc.narg(created_at), CURRENT_TIMESTAMP));

-- name: BranchUpdateCommit :exec
UPDATE branches
SET commit_id = :commit_id, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND deleted = FALSE;
-- name: ProjectChunkSizes :many
SELECT hash, size_bytes
FROM chunks
WHERE project_id = sqlc.arg(project_id) AND hash IN (sqlc.slice('hashes'));

-- name: ChunkListByHashesForProject :many
SELECT id, hash
FROM chunks
WHERE project_id = sqlc.arg(project_id) AND hash IN (sqlc.slice('hashes'));

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

-- name: ChunkInsertManyOrIgnore :exec
INSERT INTO chunks (project_id, hash, size_bytes, pack_id, pack_offset)
SELECT sqlc.arg(project_id), unhex(json_extract(value, '$[0]')), json_extract(value, '$[1]'),
       json_extract(value, '$[2]'), json_extract(value, '$[3]')
FROM json_each(sqlc.arg(chunk_rows))
WHERE true
ON CONFLICT(project_id, hash) DO NOTHING;

-- name: ProjectChunkUploadInsertMany :exec
INSERT INTO project_chunk_uploads (project_id, hash)
SELECT sqlc.arg(project_id), unhex(value)
FROM json_each(sqlc.arg(hashes))
WHERE true
ON CONFLICT(project_id, hash) DO NOTHING;

-- name: ProjectChunkRecords :many
SELECT c.hash, c.size_bytes, c.pack_id, c.pack_offset, p.state AS pack_state
FROM chunks c
LEFT JOIN chunk_packs p ON p.id = c.pack_id
WHERE c.project_id = sqlc.arg(project_id) AND c.hash IN (sqlc.slice('hashes'));

-- name: ChunkRelocate :execrows
UPDATE chunks
SET pack_id = sqlc.narg(new_pack_id), pack_offset = sqlc.narg(new_pack_offset)
WHERE project_id = sqlc.arg(project_id) AND hash = sqlc.arg(hash)
  AND pack_id IS sqlc.narg(old_pack_id) AND pack_offset IS sqlc.narg(old_pack_offset);

-- name: ChunkPackInsert :exec
INSERT INTO chunk_packs (id, project_id, size_bytes, chunk_count, state)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(size_bytes), sqlc.arg(chunk_count), sqlc.arg(state));

-- name: ChunkPackSetState :execrows
UPDATE chunk_packs SET state = sqlc.arg(state) WHERE id = sqlc.arg(id);
