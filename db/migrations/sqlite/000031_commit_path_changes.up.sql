-- Path change index: the paths each commit changed against its first parent,
-- read by the per-path history (Branch.PathCommitLog: blame, file and folder
-- history) instead of comparing trees commit by commit. See
-- docs/internals/server.md "Path change index".
--
-- kind: 1 a file appeared, disappeared or changed content; 2 a file kept its
-- content and changed its mode class; 3 a directory appeared or disappeared.
-- A root commit (no first parent) has no rows: history lists it whenever the
-- walk reaches it. Rows are written with the commit; commits stored before
-- this table are indexed on first read.
CREATE TABLE commit_path_changes (
    commit_id INTEGER NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    kind INTEGER NOT NULL CHECK (kind IN (1, 2, 3)),
    PRIMARY KEY (commit_id, path, kind)
) WITHOUT ROWID;

-- One row per commit whose changes are in commit_path_changes (a commit that
-- changed nothing has a marker and no rows). A commit without a marker
-- predates the index and is indexed by the next read that walks over it.
CREATE TABLE commit_path_index (
    commit_id INTEGER PRIMARY KEY REFERENCES commits(id) ON DELETE CASCADE
);
