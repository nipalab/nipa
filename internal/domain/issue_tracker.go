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
