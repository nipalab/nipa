package model

type ProjectResponse struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DeletedProjectResponse is a deleted project that can still be restored.
// PurgeAfter is when the purge removes it for good; empty when deleted
// projects are kept until restored.
type DeletedProjectResponse struct {
	ID         string `json:"id"`
	OrgID      string `json:"org_id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	DeletedAt  string `json:"deleted_at"`
	PurgeAfter string `json:"purge_after,omitempty"`
}

type CreateProjectRequest struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Slug            string `json:"slug"`
	StorageServerID string `json:"storage_server_id,omitempty"`
}

type UpdateProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
