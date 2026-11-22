-- Tree hash backfill (usecase.BackfillTreeHashes) and the legacy root hashes
-- it records.

-- name: TreeHashBackfillWatermark :one
SELECT watermark
FROM tree_hash_backfill
WHERE id = 1;

-- name: TreeHashBackfillSetWatermark :exec
UPDATE tree_hash_backfill
SET watermark = sqlc.arg(watermark), updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- name: TreeNodeMaxID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) AS max_id
FROM tree_nodes;

-- name: TreeHashSeeds :many
SELECT DISTINCT CAST(tree_id AS INTEGER) AS tree_id
FROM files
WHERE tree_id > sqlc.arg(after_id)
  AND tree_id <= sqlc.arg(up_to_id)
  AND mode NOT IN (0, 420)
ORDER BY tree_id;

-- name: TreeHashNode :one
SELECT t.id, t.parent_tree_id, t.name, t.hash,
       CAST(EXISTS (SELECT 1 FROM files f WHERE f.tree_id = t.id)
            OR EXISTS (SELECT 1 FROM tree_nodes c WHERE c.parent_tree_id = t.id) AS INTEGER) AS has_content
FROM tree_nodes t
WHERE t.id = sqlc.arg(id);

-- name: TreeHashFiles :many
SELECT name, hash, mode
FROM files
WHERE tree_id = sqlc.arg(tree_id);

-- name: TreeHashChildren :many
SELECT id, parent_tree_id, name, hash
FROM tree_nodes
WHERE parent_tree_id = sqlc.arg(parent_tree_id);

-- name: TreeHashNodesByHash :many
SELECT t.id, t.parent_tree_id, t.name, t.hash,
       CAST(EXISTS (SELECT 1 FROM files f WHERE f.tree_id = t.id)
            OR EXISTS (SELECT 1 FROM tree_nodes c WHERE c.parent_tree_id = t.id) AS INTEGER) AS has_content
FROM tree_nodes t
WHERE t.hash = sqlc.arg(hash)
ORDER BY t.id;

-- name: TreeNodeSetHash :exec
UPDATE tree_nodes
SET hash = sqlc.arg(hash)
WHERE id = sqlc.arg(id);

-- name: TreeHashLegacyInsert :exec
INSERT INTO tree_hash_legacy (tree_id, hash)
VALUES (sqlc.arg(tree_id), sqlc.arg(hash))
ON CONFLICT (tree_id) DO NOTHING;

-- name: TreeHashLegacyGet :one
SELECT hash
FROM tree_hash_legacy
WHERE tree_id = sqlc.arg(tree_id);
