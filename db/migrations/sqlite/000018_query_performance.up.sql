CREATE INDEX idx_pbac_rules_org_wide ON pbac_rules (org_id) WHERE project_id IS NULL;

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id);

CREATE INDEX idx_merge_requests_source_status ON merge_requests (source_branch_id, status);

CREATE INDEX idx_merge_requests_target_status ON merge_requests (target_branch_id, status);

CREATE INDEX idx_email_deliveries_thread ON email_deliveries (project_id, thread_key, user_id);

CREATE INDEX idx_email_deliveries_sweep ON email_deliveries (updated_at) WHERE state IN ('delivered', 'failed');

DROP INDEX idx_branches_project_updated_id;

CREATE INDEX idx_branches_project_updated_id
    ON branches (project_id, updated_at DESC, id DESC)
    WHERE deleted = FALSE;

-- Covered by UNIQUE constraints, unique indexes or the rowid primary key.
DROP INDEX idx_tree_nodes_parent;

DROP INDEX idx_files_tree_id;

DROP INDEX idx_merge_requests_project;

DROP INDEX idx_merge_request_review_requests_mr;

DROP INDEX idx_merge_request_checks_mr;

DROP INDEX idx_audit_log_id;
