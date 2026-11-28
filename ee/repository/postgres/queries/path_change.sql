-- CommitPathChangeInsertMany writes path change index rows (see
-- docs/internals/server.md "Path change index") from a JSON array of
-- [path, kind] pairs.

-- name: CommitPathChangeInsertMany :exec
INSERT INTO commit_path_changes (commit_id, path, kind)
SELECT sqlc.arg(commit_id)::bigint, r->>0, (r->>1)::bigint
FROM jsonb_array_elements(sqlc.arg(changes)::jsonb) AS r
ON CONFLICT (commit_id, path, kind) DO NOTHING;

-- name: CommitPathIndexMark :exec
INSERT INTO commit_path_index (commit_id) VALUES (sqlc.arg(commit_id))
ON CONFLICT (commit_id) DO NOTHING;

-- PathChangeLog walks the first-parent chain from start_commit_id (at most
-- scan_limit commits, like CommitLog) and keeps the commits that changed the
-- path: the root commit of the chain, a row at the path itself other than a
-- mode-only change of a file there, any row below the path (the byte range
-- [path/, path0) under the column's "C" collation), or any row at all when
-- any_path is set (the whole tree). Every probe is a primary key range seek
-- per walked commit.
-- name: PathChangeLog :many
WITH RECURSIVE commit_chain(id, depth) AS (
    SELECT sqlc.arg(start_commit_id)::bigint AS id, 0::bigint AS depth
    UNION ALL
    SELECT c.parent_1_id, cc.depth + 1 FROM commits c JOIN commit_chain cc ON c.id = cc.id
    WHERE c.parent_1_id IS NOT NULL AND cc.depth + 1 < sqlc.arg(scan_limit)::bigint
)
SELECT c.id, c.hash, c.project_id, c.tree_id, c.parent_1_id, c.parent_2_id, c.user_id, c.message, c.created_at,
       u.name AS author_name, u.email AS author_email,
       c.author_name AS commit_author_name, c.author_email AS commit_author_email, c.origin
FROM commits c
JOIN users u ON c.user_id = u.id
JOIN commit_chain cc ON c.id = cc.id
WHERE c.project_id = sqlc.arg(project_id)
  AND (
      c.parent_1_id IS NULL
      OR (sqlc.arg(any_path)::boolean AND EXISTS (SELECT 1 FROM commit_path_changes x WHERE x.commit_id = c.id))
      OR EXISTS (SELECT 1 FROM commit_path_changes x WHERE x.commit_id = c.id AND x.path = sqlc.arg(path)::text AND x.kind <> 2)
      OR EXISTS (SELECT 1 FROM commit_path_changes x WHERE x.commit_id = c.id AND x.path >= sqlc.arg(prefix_from)::text AND x.path < sqlc.arg(prefix_to)::text)
  )
ORDER BY cc.depth ASC, c.id DESC
LIMIT sqlc.arg(row_limit)::bigint;

-- PathChangeRows returns the rows of the given commits at or below the path
-- (every row when any_path is set), for a reader whose rules hide paths.
-- name: PathChangeRows :many
SELECT x.commit_id, x.path, x.kind
FROM commit_path_changes x
WHERE x.commit_id = ANY(sqlc.arg(commit_ids)::bigint[])
  AND (sqlc.arg(any_path)::boolean OR x.path = sqlc.arg(path)::text
       OR (x.path >= sqlc.arg(prefix_from)::text AND x.path < sqlc.arg(prefix_to)::text))
ORDER BY x.commit_id, x.path, x.kind;

-- PathChangeUnindexed lists the commits of the same first-parent walk that
-- have a parent and no commit_path_index marker yet (stored before the index
-- existed), with their tree and their first parent's tree.
-- name: PathChangeUnindexed :many
WITH RECURSIVE commit_chain(id, depth) AS (
    SELECT sqlc.arg(start_commit_id)::bigint AS id, 0::bigint AS depth
    UNION ALL
    SELECT c.parent_1_id, cc.depth + 1 FROM commits c JOIN commit_chain cc ON c.id = cc.id
    WHERE c.parent_1_id IS NOT NULL AND cc.depth + 1 < sqlc.arg(scan_limit)::bigint
)
SELECT c.id, c.tree_id, p.tree_id AS parent_tree_id
FROM commits c
JOIN commit_chain cc ON c.id = cc.id
JOIN commits p ON p.id = c.parent_1_id
WHERE c.project_id = sqlc.arg(project_id)
  AND NOT EXISTS (SELECT 1 FROM commit_path_index i WHERE i.commit_id = c.id)
ORDER BY cc.depth ASC;
