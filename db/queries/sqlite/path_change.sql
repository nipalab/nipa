-- CommitPathChangeInsertMany writes path change index rows (see
-- docs/internals/server.md "Path change index") from a JSON array of
-- [path, kind] pairs.

-- name: CommitPathChangeInsertMany :exec
INSERT INTO commit_path_changes (commit_id, path, kind)
SELECT sqlc.arg(commit_id), json_extract(value, '$[0]'), json_extract(value, '$[1]')
FROM json_each(sqlc.arg(changes))
WHERE true
ON CONFLICT(commit_id, path, kind) DO NOTHING;

-- name: CommitPathIndexMark :exec
INSERT INTO commit_path_index (commit_id) VALUES (sqlc.arg(commit_id))
ON CONFLICT(commit_id) DO NOTHING;

-- PathChangeLog walks the first-parent chain from start_commit_id (at most
-- scan_limit commits, like CommitLog) and keeps the commits that changed the
-- path: the root commit of the chain, a row at the path itself other than a
-- mode-only change of a file there, any row below the path (the byte range
-- [path/, path0)), or any row at all when any_path is set (the whole tree).
-- Every probe is a primary key range seek per walked commit. scan_limit is
-- cast so sqlc types it without resolving the recursive alias.
-- name: PathChangeLog :many
WITH RECURSIVE commit_chain(id, depth) AS (
    SELECT :start_commit_id AS id, 0 AS depth
    UNION ALL
    SELECT c.parent_1_id, cc.depth + 1 FROM commits c JOIN commit_chain cc ON c.id = cc.id
    WHERE c.parent_1_id IS NOT NULL AND cc.depth + 1 < CAST(:scan_limit AS INTEGER)
)
SELECT c.id, c.hash, c.project_id, c.tree_id, c.parent_1_id, c.parent_2_id, c.user_id, c.message, c.created_at,
       u.name AS author_name, u.email AS author_email,
       c.author_name AS commit_author_name, c.author_email AS commit_author_email, c.origin
FROM commits c
JOIN users u ON c.user_id = u.id
JOIN commit_chain cc ON c.id = cc.id
WHERE c.project_id = :project_id
  AND (
      c.parent_1_id IS NULL
      OR (:any_path = 1 AND EXISTS (SELECT 1 FROM commit_path_changes x WHERE x.commit_id = c.id))
      OR EXISTS (SELECT 1 FROM commit_path_changes x WHERE x.commit_id = c.id AND x.path = :path AND x.kind <> 2)
      OR EXISTS (SELECT 1 FROM commit_path_changes x WHERE x.commit_id = c.id AND x.path >= :prefix_from AND x.path < :prefix_to)
  )
ORDER BY cc.depth ASC, c.id DESC
LIMIT :row_limit;

-- PathChangeRows returns the rows of the given commits at or below the path
-- (every row when any_path is set), for a reader whose rules hide paths. The
-- slice comes last: sqlc expands it into bare "?" placeholders, which SQLite
-- numbers after the highest ?N before them, so a slice ahead of the numbered
-- parameters would shift their bindings.
-- name: PathChangeRows :many
SELECT x.commit_id, x.path, x.kind
FROM commit_path_changes x
WHERE (:any_path = 1 OR x.path = :path OR (x.path >= :prefix_from AND x.path < :prefix_to))
  AND x.commit_id IN (sqlc.slice('commit_ids'))
ORDER BY x.commit_id, x.path, x.kind;

-- PathChangeUnindexed lists the commits of the same first-parent walk that
-- have a parent and no commit_path_index marker yet (stored before the index
-- existed), with their tree and their first parent's tree.
-- name: PathChangeUnindexed :many
WITH RECURSIVE commit_chain(id, depth) AS (
    SELECT :start_commit_id AS id, 0 AS depth
    UNION ALL
    SELECT c.parent_1_id, cc.depth + 1 FROM commits c JOIN commit_chain cc ON c.id = cc.id
    WHERE c.parent_1_id IS NOT NULL AND cc.depth + 1 < CAST(:scan_limit AS INTEGER)
)
SELECT c.id, c.tree_id, p.tree_id AS parent_tree_id
FROM commits c
JOIN commit_chain cc ON c.id = cc.id
JOIN commits p ON p.id = c.parent_1_id
WHERE c.project_id = :project_id
  AND NOT EXISTS (SELECT 1 FROM commit_path_index i WHERE i.commit_id = c.id)
ORDER BY cc.depth ASC;
