-- name: MetaGet :one
SELECT value
FROM meta
WHERE key = :key
LIMIT 1;

-- name: MetaSet :exec
INSERT INTO meta (key, value)
VALUES (:key, :value)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: MetaDelete :exec
DELETE FROM meta
WHERE key = :key;

-- name: TreeNodeUpsert :exec
INSERT INTO tree_nodes (path, parent_path, hash, mode, snapshot_id)
VALUES (:path, :parent_path, :hash, :mode, :snapshot_id)
ON CONFLICT(path) DO UPDATE SET
    parent_path = excluded.parent_path,
    hash = excluded.hash,
    mode = excluded.mode,
    snapshot_id = excluded.snapshot_id;

-- name: FileUpsert :exec
INSERT INTO files (path, tree_path, hash, size_bytes, mode, is_binary, snapshot_id)
VALUES (:path, :tree_path, :hash, :size_bytes, :mode, :is_binary, :snapshot_id)
ON CONFLICT(path) DO UPDATE SET
    tree_path = excluded.tree_path,
    hash = excluded.hash,
    size_bytes = excluded.size_bytes,
    mode = excluded.mode,
    is_binary = excluded.is_binary,
    snapshot_id = excluded.snapshot_id;

-- name: StaleFileDelete :exec
DELETE FROM files
WHERE snapshot_id <> :snapshot_id;

-- name: StaleTreeNodeDelete :exec
DELETE FROM tree_nodes
WHERE snapshot_id <> :snapshot_id;

-- name: StagedFileList :many
SELECT path
FROM staged_files
ORDER BY path;

-- name: StagedFileInsert :exec
INSERT INTO staged_files (path)
VALUES (:path)
ON CONFLICT(path) DO NOTHING;

-- name: StagedFileDelete :exec
DELETE FROM staged_files
WHERE path = :path;

-- name: StagedFileDeleteAll :exec
DELETE FROM staged_files
WHERE path IS NOT NULL;

-- name: SnapshotFileList :many
SELECT path, hash, size_bytes, mode, is_binary
FROM files
WHERE snapshot_id = :snapshot_id
ORDER BY path;