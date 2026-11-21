-- Indexes the project purge needs. Deleting a commit runs a foreign-key check
-- on every column that references commits(id); without an index each check
-- scans the whole referencing table across all projects.
CREATE INDEX IF NOT EXISTS idx_commits_project_id ON commits (project_id, id);
CREATE INDEX IF NOT EXISTS idx_commits_parent_1 ON commits (parent_1_id) WHERE parent_1_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_commits_parent_2 ON commits (parent_2_id) WHERE parent_2_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_branches_commit_id ON branches (commit_id) WHERE commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tags_commit_id ON tags (commit_id);
CREATE INDEX IF NOT EXISTS idx_merge_requests_merge_commit ON merge_requests (merge_commit_id) WHERE merge_commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_merge_requests_merge_base_commit ON merge_requests (merge_base_commit_id) WHERE merge_base_commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_merge_requests_merged_source_commit ON merge_requests (merged_source_commit_id) WHERE merged_source_commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_merge_request_checks_head_commit ON merge_request_checks (head_commit_id);
CREATE INDEX IF NOT EXISTS idx_merge_request_reviews_head_commit ON merge_request_reviews (head_commit_id);
CREATE INDEX IF NOT EXISTS idx_merge_request_threads_base_commit ON merge_request_threads (base_commit_id) WHERE base_commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_merge_request_threads_head_commit ON merge_request_threads (head_commit_id) WHERE head_commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_merge_request_events_commit ON merge_request_events (commit_id) WHERE commit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_file_locks_merge_request ON file_locks (merge_request_id) WHERE merge_request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects (deleted_at) WHERE deleted;
