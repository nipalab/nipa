package domain

// CommitLogEntry extends Commit with author information for log display.
type CommitLogEntry struct {
	Commit
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
}
