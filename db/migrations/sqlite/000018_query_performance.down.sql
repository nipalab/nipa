CREATE INDEX idx_audit_log_id ON audit_log (id DESC);

CREATE INDEX idx_merge_request_checks_mr ON merge_request_checks (merge_request_id, head_commit_id);

CREATE INDEX idx_merge_request_review_requests_mr
    ON merge_request_review_requests (merge_request_id);

CREATE INDEX idx_merge_requests_project ON merge_requests (project_id, id DESC);

CREATE INDEX idx_files_tree_id ON files(tree_id);

CREATE INDEX idx_tree_nodes_parent ON tree_nodes(parent_tree_id);

DROP INDEX idx_branches_project_updated_id;

CREATE INDEX idx_branches_project_updated_id
    ON branches (project_id, updated_at DESC, id DESC);

DROP INDEX idx_email_deliveries_sweep;

DROP INDEX idx_email_deliveries_thread;

DROP INDEX idx_merge_requests_target_status;

DROP INDEX idx_merge_requests_source_status;

DROP INDEX idx_refresh_tokens_user;

DROP INDEX idx_pbac_rules_org_wide;
