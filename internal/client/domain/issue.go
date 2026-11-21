package domain

const (
	IssueStateOK          = "ok"
	IssueStateNotFound    = "not_found"
	IssueStateUnavailable = "unavailable"
)

// Issue is an issue of the project's tracker; a tracker that is down leaves
// everything but Key, URL and State empty.
type Issue struct {
	Key            string `json:"key"`
	URL            string `json:"url"`
	State          string `json:"state"`
	Title          string `json:"title,omitempty"`
	Type           string `json:"type,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusCategory string `json:"status_category,omitempty"`
	Assignee       string `json:"assignee,omitempty"`
}

type IssueSearch struct {
	Issues      []Issue `json:"issues"`
	Supported   bool    `json:"supported"`
	Unavailable bool    `json:"unavailable,omitempty"`
}
