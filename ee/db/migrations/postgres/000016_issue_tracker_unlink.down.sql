DELETE FROM issue_tracker_jobs WHERE action = 'unlink';
ALTER TABLE issue_tracker_jobs DROP CONSTRAINT issue_tracker_jobs_action_check;
ALTER TABLE issue_tracker_jobs ADD CONSTRAINT issue_tracker_jobs_action_check
    CHECK (action IN ('link', 'comment', 'transition'));
