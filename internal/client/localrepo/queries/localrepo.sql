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
INSERT INTO files (path, tree_path, hash, size_bytes, mode, is_binary, encoding, chunks, snapshot_id)
VALUES (:path, :tree_path, :hash, :size_bytes, :mode, :is_binary, :encoding, :chunks, :snapshot_id)
ON CONFLICT(path) DO UPDATE SET
    tree_path = excluded.tree_path,
    hash = excluded.hash,
    size_bytes = excluded.size_bytes,
    mode = excluded.mode,
    is_binary = excluded.is_binary,
    encoding = excluded.encoding,
    chunks = excluded.chunks,
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
SELECT path, hash, size_bytes, mode, is_binary, encoding, chunks
FROM files
WHERE snapshot_id = :snapshot_id
ORDER BY path;

-- name: StatCacheList :many
SELECT path, size_bytes, mtime_ns, mode, hash, cached_at
FROM stat_cache
ORDER BY path;

-- name: StatCacheUpsert :exec
INSERT INTO stat_cache (path, size_bytes, mtime_ns, mode, hash, cached_at)
VALUES (:path, :size_bytes, :mtime_ns, :mode, :hash, :cached_at)
ON CONFLICT(path) DO UPDATE SET
    size_bytes = excluded.size_bytes,
    mtime_ns = excluded.mtime_ns,
    mode = excluded.mode,
    hash = excluded.hash,
    cached_at = excluded.cached_at;

-- name: StatCacheSweep :exec
DELETE FROM stat_cache
WHERE path NOT IN (SELECT ltrim(path, '/') FROM files);