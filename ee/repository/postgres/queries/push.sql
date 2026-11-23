-- name: TreeNodeInsert :one
INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (sqlc.arg(hash), sqlc.arg(name), sqlc.arg(mode), sqlc.arg(parent_tree_id))
RETURNING id;

-- name: TreeNodeSetParent :exec
UPDATE tree_nodes
SET parent_tree_id = sqlc.arg(parent_tree_id)
WHERE id = sqlc.arg(id);

-- name: FileSetTree :exec
UPDATE files
SET tree_id = sqlc.arg(tree_id)
WHERE id = sqlc.arg(id);

-- name: CommitInsert :exec
INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, parent_2_id, user_id, message, author_name, author_email, origin, created_at)
VALUES (sqlc.arg(id), sqlc.arg(hash), sqlc.arg(project_id), sqlc.arg(tree_id), sqlc.arg(parent_1_id), sqlc.arg(parent_2_id), sqlc.arg(user_id), sqlc.arg(message),
        sqlc.narg(author_name), sqlc.narg(author_email), sqlc.narg(origin), COALESCE(sqlc.narg(created_at)::timestamptz, now()));

-- name: BranchUpdateCommit :exec
UPDATE branches
SET commit_id = sqlc.arg(commit_id), updated_at = now()
WHERE id = sqlc.arg(id) AND deleted = FALSE;

-- name: ProjectChunkSizes :many
SELECT hash, size_bytes
FROM chunks
WHERE project_id = sqlc.arg(project_id) AND hash = ANY(sqlc.arg(hashes)::bytea[]);

-- name: ChunkListByHashesForProject :many
SELECT id, hash
FROM chunks
WHERE project_id = sqlc.arg(project_id) AND hash = ANY(sqlc.arg(hashes)::bytea[]);

-- name: FileInsertMany :many
INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary, encoding)
SELECT r->>'name', (r->>'mode')::bigint, (r->>'tree_id')::bigint, decode(r->>'hash', 'hex'),
       (r->>'size_bytes')::bigint, (r->>'is_binary')::boolean, r->>'encoding'
FROM jsonb_array_elements(sqlc.arg(file_rows)::jsonb) AS r
RETURNING id, tree_id, name;

-- name: FileChunkInsertMany :exec
INSERT INTO file_chunks (file_id, chunk_id, chunk_index)
SELECT (r->>0)::bigint, (r->>1)::bigint, (r->>2)::bigint
FROM jsonb_array_elements(sqlc.arg(links)::jsonb) AS r
ON CONFLICT (file_id, chunk_index) DO UPDATE SET chunk_id = excluded.chunk_id;

-- name: ChunkInsertManyOrIgnore :exec
INSERT INTO chunks (project_id, hash, size_bytes, pack_id, pack_offset)
SELECT sqlc.arg(project_id)::bigint, decode(r->>0, 'hex'), (r->>1)::bigint, (r->>2)::bigint, (r->>3)::bigint
FROM jsonb_array_elements(sqlc.arg(chunk_rows)::jsonb) AS r
ON CONFLICT (project_id, hash) DO NOTHING;

-- name: ProjectChunkUploadInsertMany :exec
INSERT INTO project_chunk_uploads (project_id, hash)
SELECT sqlc.arg(project_id)::bigint, decode(h, 'hex')
FROM jsonb_array_elements_text(sqlc.arg(hashes)::jsonb) AS h
ON CONFLICT (project_id, hash) DO NOTHING;

-- name: ProjectChunkRecords :many
SELECT c.hash, c.size_bytes, c.pack_id, c.pack_offset, p.state AS pack_state
FROM chunks c
LEFT JOIN chunk_packs p ON p.id = c.pack_id
WHERE c.project_id = sqlc.arg(project_id) AND c.hash = ANY(sqlc.arg(hashes)::bytea[]);

-- name: ChunkRelocate :execrows
UPDATE chunks
SET pack_id = sqlc.narg(new_pack_id)::bigint, pack_offset = sqlc.narg(new_pack_offset)::bigint
WHERE project_id = sqlc.arg(project_id) AND hash = sqlc.arg(hash)
  AND pack_id IS NOT DISTINCT FROM sqlc.narg(old_pack_id)::bigint
  AND pack_offset IS NOT DISTINCT FROM sqlc.narg(old_pack_offset)::bigint;

-- name: ChunkPackInsert :exec
INSERT INTO chunk_packs (id, project_id, size_bytes, chunk_count, state)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(size_bytes), sqlc.arg(chunk_count), sqlc.arg(state));

-- name: ChunkPackSetState :execrows
UPDATE chunk_packs SET state = sqlc.arg(state) WHERE id = sqlc.arg(id);
