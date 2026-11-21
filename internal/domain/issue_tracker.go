package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

const (
	LinkCommitsOff           = "off"
	LinkCommitsDefaultBranch = "default_branch"
	LinkCommitsAll           = "all"
)

// IssueTracker is an org's connection to an external issue tracker. The
// secret is sealed and never leaves the server.
type IssueTracker struct {
	ID           snow.ID
	OrgID        snow.ID
	Kind         string
	Name         string
	BaseURL      string
	AuthKind     string
	Account      string
	SecretSealed []byte
	CreatedBy    snow.ID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ProjectIssueTracker is a project's use of its org's connection.
type ProjectIssueTracker struct {
	ProjectID         snow.ID
	TrackerID         snow.ID
	Keys              []string
	LinkMergeRequests bool
	LinkCommits       string
	CommentOnMerge    bool
	MergedStatus      string
	OpenedStatus      string
	IsActive          bool
	SetBy             snow.ID
	UpdatedAt         time.Time
}

func IsValidLinkCommits(v string) bool {
	return v == LinkCommitsOff || v == LinkCommitsDefaultBranch || v == LinkCommitsAll
}

const (
	IssueSubjectCommit       = "commit"
	IssueSubjectMergeRequest = "merge_request"
	IssueSubjectBranch       = "branch"
)

// IssueLink ties a commit, merge request or branch to an issue key.
type IssueLink struct {
	ID          snow.ID
	ProjectID   snow.ID
	TrackerID   snow.ID
	IssueKey    string
	SubjectKind string
	SubjectID   snow.ID
	Relation    string
	CreatedAt   time.Time
}

// IssueLinkSubject is a link with what is needed to render its subject;
// Missing marks a subject that no longer exists (a deleted branch).
type IssueLinkSubject struct {
	IssueLink
	CommitMessage   string
	CommitCreatedAt time.Time
	MRNumber        int64
	MRTitle         string
	MRStatus        string
	BranchName      string
	Missing         bool
}

const (
	IssueJobLink       = "link"
	IssueJobComment    = "comment"
	IssueJobTransition = "transition"
	IssueJobUnlink     = "unlink"

	IssueJobPending   = "pending"
	IssueJobDelivered = "delivered"
	IssueJobFailed    = "failed"
)

// IssueTrackerJob is one outbound tracker call waiting in the outbox. A
// pending job is due at NextRetryAt; a claimed one carries its lease there.
type IssueTrackerJob struct {
	ID          snow.ID
	TrackerID   snow.ID
	ProjectID   snow.ID
	IssueKey    string
	Action      string
	Payload     string
	DedupeKey   string
	State       string
	Attempt     int64
	LastError   string
	NextRetryAt *time.Time
	DeliveredAt *time.Time
	CreatedAt   time.Time
}

func IsValidIssueJobState(state string) bool {
	return state == IssueJobPending || state == IssueJobDelivered || state == IssueJobFailed
}
