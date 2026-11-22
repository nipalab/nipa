-- Tree hash backfill (usecase.BackfillTreeHashes) and the legacy root hashes
-- it records.

-- name: TreeHashBackfillWatermark :one
SELECT watermark
FROM tree_hash_backfill
WHERE id = 1;

-- name: TreeHashBackfillSetWatermark :exec
UPDATE tree_hash_backfill
SET watermark = sqlc.arg(watermark), started_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- TreeHashBackfillRescanFrom returns the id of the newest tree node at or below
-- the watermark created before the previous pass started minus the overlap:
-- rows above it may have committed after that pass. It walks the primary key
-- backwards from the watermark, so it only reads the rows of the window.
-- name: TreeHashBackfillRescanFrom :one
SELECT CAST(COALESCE((
    SELECT t.id
    FROM tree_nodes t
    WHERE b.started_at IS NOT NULL
      AND t.id <= b.watermark
      AND (t.created_at IS NULL
           OR t.created_at < datetime(b.started_at, printf('-%d seconds', CAST(sqlc.arg(overlap_seconds) AS INTEGER))))
    ORDER BY t.id DESC
    LIMIT 1
), 0) AS INTEGER) AS rescan_from
FROM tree_hash_backfill b
WHERE b.id = 1;

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

-- TreeHashDanglingReferences lists the reference rows (no files, no child
-- rows) in (after_id, up_to_id] whose hash no content node carries any more
-- but the backfill rewrote, with the new hash of the content node they
-- resolved to.
-- name: TreeHashDanglingReferences :many
SELECT t.id, t.parent_tree_id, t.name, t.hash, r.new_hash
FROM tree_nodes t
JOIN tree_hash_rewrites r ON r.old_hash = t.hash
WHERE t.id > sqlc.arg(after_id)
  AND t.id <= sqlc.arg(up_to_id)
  AND NOT EXISTS (SELECT 1 FROM files f WHERE f.tree_id = t.id)
  AND NOT EXISTS (SELECT 1 FROM tree_nodes c WHERE c.parent_tree_id = t.id)
  AND NOT EXISTS (
      SELECT 1
      FROM tree_nodes content
      WHERE content.hash = t.hash
        AND (EXISTS (SELECT 1 FROM files cf WHERE cf.tree_id = content.id)
             OR EXISTS (SELECT 1 FROM tree_nodes cc WHERE cc.parent_tree_id = content.id))
  )
ORDER BY t.id;

-- name: TreeHashRewriteInsert :exec
INSERT INTO tree_hash_rewrites (old_hash, new_hash)
VALUES (sqlc.arg(old_hash), sqlc.arg(new_hash))
ON CONFLICT (old_hash) DO NOTHING;

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
