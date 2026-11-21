package output

import (
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type Log struct {
	Commits []LogCommit `json:"commits"`
}

type LogCommit struct {
	ID          string                     `json:"id"`
	Hash        string                     `json:"hash"`
	Message     string                     `json:"message"`
	AuthorName  string                     `json:"author_name,omitempty"`
	AuthorEmail string                     `json:"author_email,omitempty"`
	Origin      string                     `json:"origin,omitempty"`
	CreatedAt   string                     `json:"created_at"`
	ParentIDs   []string                   `json:"parent_ids"`
	IssueKeys   []serverDomain.IssueKeyRef `json:"issue_keys,omitempty"`
}

func NewLog(entries []*serverDomain.CommitLogEntry) Log {
	out := Log{Commits: make([]LogCommit, 0, len(entries))}
	for _, e := range entries {
		if e == nil {
			continue
		}
		authorName, authorEmail := e.DisplayAuthor()
		out.Commits = append(out.Commits, LogCommit{
			ID:          e.ID.Base36(),
			Hash:        e.Hash.String(),
			Message:     e.Message,
			AuthorName:  authorName,
			AuthorEmail: authorEmail,
			Origin:      e.Origin,
			CreatedAt:   e.CreatedAt.UTC().Format(time.RFC3339),
			ParentIDs:   parentIDs(e),
			IssueKeys:   e.IssueKeys,
		})
	}
	return out
}

func parentIDs(e *serverDomain.CommitLogEntry) []string {
	ids := []string{}
	if e.Parent1ID != nil {
		ids = append(ids, e.Parent1ID.Base36())
	}
	if e.Parent2ID != nil {
		ids = append(ids, e.Parent2ID.Base36())
	}
	return ids
}
