-- merge_request_approvals (000007) was never read or written; it is replaced by
-- merge_request_reviews, which carries a decision state and the source head the
-- review was given for so stale reviews can be dismissed without losing history.

DROP INDEX IF EXISTS idx_merge_request_approvals_mr;

DROP TABLE IF EXISTS merge_request_approvals;

CREATE TABLE merge_request_reviews (
    id               INTEGER PRIMARY KEY,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    reviewer_id      INTEGER NOT NULL REFERENCES users(id),
    state            TEXT NOT NULL,
    body             TEXT NOT NULL DEFAULT '',
    head_commit_id   INTEGER NOT NULL REFERENCES commits(id),
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dismissed_at     DATETIME,
    dismissed_by     INTEGER REFERENCES users(id),
    dismissed_reason TEXT,
    CHECK (state IN ('commented', 'approved', 'changes_requested'))
);

-- one review per reviewer per review round (head commit)
CREATE UNIQUE INDEX idx_merge_request_reviews_round
    ON merge_request_reviews (merge_request_id, reviewer_id, head_commit_id);

CREATE INDEX idx_merge_request_reviews_mr
    ON merge_request_reviews (merge_request_id, id);

CREATE TABLE merge_request_review_requests (
    id               INTEGER PRIMARY KEY,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    reviewer_id      INTEGER NOT NULL REFERENCES users(id),
    requested_by     INTEGER NOT NULL REFERENCES users(id),
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (merge_request_id, reviewer_id)
);

CREATE INDEX idx_merge_request_review_requests_mr
    ON merge_request_review_requests (merge_request_id);

ALTER TABLE merge_request_threads
    ADD COLUMN review_id INTEGER REFERENCES merge_request_reviews(id) ON DELETE SET NULL;

CREATE INDEX idx_merge_request_threads_position
    ON merge_request_threads (merge_request_id, file_path, new_line);

CREATE INDEX idx_merge_request_threads_unresolved
    ON merge_request_threads (merge_request_id) WHERE resolved = 0;

CREATE TABLE merge_request_events (
    id               INTEGER PRIMARY KEY,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    actor_id         INTEGER NOT NULL REFERENCES users(id),
    -- the user an event is about, such as the reviewer a request names
    subject_user_id  INTEGER REFERENCES users(id),
    kind             TEXT NOT NULL,
    body             TEXT NOT NULL DEFAULT '',
    commit_id        INTEGER REFERENCES commits(id),
    commit_hash      TEXT,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_request_events_mr
    ON merge_request_events (merge_request_id, id);
