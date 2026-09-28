package output

import (
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type Log struct {
	Commits []LogCommit `json:"commits"`
}

type LogCommit struct {
	ID          string   `json:"id"`
	Hash        string   `json:"hash"`
	Message     string   `json:"message"`
	AuthorName  string   `json:"author_name,omitempty"`
	AuthorEmail string   `json:"author_email,omitempty"`
	CreatedAt   string   `json:"created_at"`
	ParentIDs   []string `json:"parent_ids"`
}

func NewLog(entries []*serverDomain.CommitLogEntry) Log {
	out := Log{Commits: make([]LogCommit, 0, len(entries))}
	for _, e := range entries {
		if e == nil {
			continue
		}
		out.Commits = append(out.Commits, LogCommit{
			ID:          e.ID.Base36(),
			Hash:        e.Hash.String(),
			Message:     e.Message,
			AuthorName:  e.AuthorName,
			AuthorEmail: e.AuthorEmail,
			CreatedAt:   e.CreatedAt.UTC().Format(time.RFC3339),
			ParentIDs:   parentIDs(e),
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
