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
INSERT INTO tree_nodes (path, parent_path, hash, mode)
VALUES (:path, :parent_path, :hash, :mode)
ON CONFLICT(path) DO UPDATE SET
    parent_path = excluded.parent_path,
    hash = excluded.hash,
    mode = excluded.mode;

-- name: FileUpsert :exec
INSERT INTO files (path, tree_path, hash, size_bytes, mode, is_binary, encoding, chunks)
VALUES (:path, :tree_path, :hash, :size_bytes, :mode, :is_binary, :encoding, :chunks)
ON CONFLICT(path) DO UPDATE SET
    tree_path = excluded.tree_path,
    hash = excluded.hash,
    size_bytes = excluded.size_bytes,
    mode = excluded.mode,
    is_binary = excluded.is_binary,
    encoding = excluded.encoding,
    chunks = excluded.chunks;

-- name: TreeNodeList :many
SELECT path, parent_path, hash, mode
FROM tree_nodes;

-- name: TreeNodeDelete :exec
DELETE FROM tree_nodes
WHERE path = :path;

-- name: FileList :many
SELECT path, tree_path, hash, size_bytes, mode, is_binary, encoding, chunks
FROM files;

-- name: FileDelete :exec
DELETE FROM files
WHERE path = :path;

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

-- name: PinnedPathUpsert :exec
INSERT INTO pinned_paths (path, pinned_at)
VALUES (:path, :pinned_at)
ON CONFLICT(path) DO UPDATE SET pinned_at = excluded.pinned_at;

-- name: PinnedPathDelete :exec
DELETE FROM pinned_paths
WHERE path = :path;

-- name: PinnedPathList :many
SELECT path
FROM pinned_paths
ORDER BY path;

-- name: SnapshotUsage :one
SELECT CAST(COUNT(*) AS INTEGER) AS files, CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS size_bytes
FROM files;

-- name: TreeNodeChildren :many
SELECT path
FROM tree_nodes
WHERE parent_path = sqlc.arg(parent_path) AND path <> parent_path
ORDER BY path;

-- name: FileListByTree :many
SELECT path, hash, size_bytes, mode, is_binary, encoding, chunks
FROM files
WHERE tree_path = sqlc.arg(tree_path)
ORDER BY path;

-- name: FileGet :one
SELECT path, hash, size_bytes, mode, is_binary, encoding, chunks
FROM files
WHERE path = sqlc.arg(path)
LIMIT 1;

-- name: FileSearchPaths :many
SELECT path, size_bytes, is_binary
FROM files
WHERE path LIKE sqlc.arg(pattern) ESCAPE '\'
LIMIT sqlc.arg(max_rows);
