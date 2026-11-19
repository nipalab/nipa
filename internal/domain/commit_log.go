package domain

// CommitLogEntry extends Commit with author information for log display.
type CommitLogEntry struct {
	Commit
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
}

// DisplayAuthor returns the source author of an imported commit, else the
// Nipa user who pushed it.
func (e *CommitLogEntry) DisplayAuthor() (name, email string) {
	if e.Commit.AuthorName != "" || e.Commit.AuthorEmail != "" {
		return e.Commit.AuthorName, e.Commit.AuthorEmail
	}
	return e.AuthorName, e.AuthorEmail
}
