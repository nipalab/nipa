package model

import "time"

type OrgResponse struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type OrgMemberResponse struct {
	UserID       string     `json:"user_id"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	PhotoUrl     string     `json:"photo_url"`
	IsAdmin      bool       `json:"is_admin"`
	IsSuperAdmin bool       `json:"is_super_admin"`
	Role         string     `json:"role"`
	JoinedAt     *time.Time `json:"joined_at,omitempty"`
}

type AddOrgMemberRequest struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type UpdateOrgMemberRequest struct {
	Role string `json:"role"`
}
