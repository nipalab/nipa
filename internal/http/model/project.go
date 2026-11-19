package model

type ProjectResponse struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
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
