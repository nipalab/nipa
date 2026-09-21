package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type User struct {
	ID           snow.ID    `json:"id"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	Password     string     `json:"password"`
	PhotoUrl     string     `json:"photo_url"`
	IsSuperAdmin bool       `json:"is_super_admin"`
	IsAdmin      bool       `json:"is_admin"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Deleted      bool       `json:"deleted"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

const (
	OrgRoleOwner  = "owner"
	OrgRoleMember = "member"
)

func IsValidOrgRole(role string) bool {
	return role == OrgRoleOwner || role == OrgRoleMember
}

type OrgMember struct {
	User     User      `json:"user"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type OrgMembership struct {
	Org  Organization `json:"org"`
	Role string       `json:"role"`
}

type Group struct {
	ID          snow.ID    `json:"id"`
	OrgID       snow.ID    `json:"org_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Deleted     bool       `json:"deleted"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	MemberIDs   []snow.ID  `json:"member_ids"`
}

type RefreshToken struct {
	ID        int64     `json:"id"`
	UserID    snow.ID   `json:"user_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
