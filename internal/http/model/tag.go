package model

import "time"

type TagResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CommitID  string    `json:"commit_id"`
	Message   string    `json:"message,omitempty"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateTagRequest struct {
	Name    string `json:"name"`
	From    string `json:"from"`
	Message string `json:"message"`
}
