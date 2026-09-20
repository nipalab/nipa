package domain

type RevertState struct {
	Targets            []CommitRef `json:"targets"`
	CurrentTreeHash    string      `json:"current_tree_hash"`
	CurrentCommitID    string      `json:"current_commit_id,omitempty"`
	OriginalTreeHash   string      `json:"original_tree_hash"`
	OriginalCommitID   string      `json:"original_commit_id,omitempty"`
	OriginalCommitHash string      `json:"original_commit_hash,omitempty"`
	Mainline           int         `json:"mainline,omitempty"`
	NoCommit           bool        `json:"no_commit,omitempty"`
	Message            string      `json:"message,omitempty"`
	Conflicts          []string    `json:"conflicts,omitempty"`
}
