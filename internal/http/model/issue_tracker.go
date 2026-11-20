package model

import "time"

// IssueTrackerRequest creates or replaces the org's connection. An omitted
// token keeps the stored one.
type IssueTrackerRequest struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	BaseURL  string  `json:"base_url"`
	AuthKind string  `json:"auth_kind"`
	Account  string  `json:"account"`
	Token    *string `json:"token,omitempty"`
}

// IssueTrackerResponse never carries the token; org members who are not
// owners only get kind, name and base_url.
type IssueTrackerResponse struct {
	ID        string            `json:"id,omitempty"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url"`
	AuthKind  string            `json:"auth_kind,omitempty"`
	Account   string            `json:"account,omitempty"`
	CreatedBy string            `json:"created_by,omitempty"`
	CreatedAt *time.Time        `json:"created_at,omitempty"`
	UpdatedAt *time.Time        `json:"updated_at,omitempty"`
	CanManage bool              `json:"can_manage"`
	Site      *IssueTrackerSite `json:"site,omitempty"`
}

type IssueTrackerSite struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Deployment  string `json:"deployment"`
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name"`
}

type IssueTrackerTestResponse struct {
	OK    bool              `json:"ok"`
	Check string            `json:"check,omitempty"`
	Error string            `json:"error,omitempty"`
	Site  *IssueTrackerSite `json:"site,omitempty"`
}

// IssueTrackerProbeError is the 400 answer of a failed probe; check is auth,
// reach or tls.
type IssueTrackerProbeError struct {
	Error string `json:"error"`
	Check string `json:"check"`
}

type ProjectIssueTrackerRequest struct {
	TrackerID         string   `json:"tracker_id,omitempty"`
	Keys              []string `json:"keys"`
	LinkMergeRequests *bool    `json:"link_merge_requests,omitempty"`
	LinkCommits       string   `json:"link_commits"`
	CommentOnMerge    *bool    `json:"comment_on_merge,omitempty"`
	MergedStatus      string   `json:"merged_status"`
	OpenedStatus      string   `json:"opened_status"`
	Active            *bool    `json:"active,omitempty"`
}

type ProjectIssueTrackerResponse struct {
	TrackerID         string    `json:"tracker_id"`
	Keys              []string  `json:"keys"`
	LinkMergeRequests bool      `json:"link_merge_requests"`
	LinkCommits       string    `json:"link_commits"`
	CommentOnMerge    bool      `json:"comment_on_merge"`
	MergedStatus      string    `json:"merged_status"`
	OpenedStatus      string    `json:"opened_status"`
	Active            bool      `json:"active"`
	SetBy             string    `json:"set_by"`
	UpdatedAt         time.Time `json:"updated_at"`
}
