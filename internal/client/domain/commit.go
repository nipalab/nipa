package domain

type LocalCommit struct {
	CommitID   string `json:"commit_id,omitempty"`
	CommitHash string `json:"commit_hash,omitempty"`
}
