package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type Tag struct {
	ID        snow.ID   `json:"id"`
	ProjectID snow.ID   `json:"project_id"`
	Name      string    `json:"name"`
	CommitID  snow.ID   `json:"commit_id"`
	Message   string    `json:"message"`
	UserID    snow.ID   `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
