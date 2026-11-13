package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type User struct {
	ID              snow.ID    `json:"id"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	Password        string     `json:"password"`
	PhotoUrl        string     `json:"photo_url"`
	IsSuperAdmin    bool       `json:"is_super_admin"`
	IsAdmin         bool       `json:"is_admin"`
	NotifyEmail     bool       `json:"notify_email"`
	EmailVerified   bool       `json:"email_verified"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Deleted         bool       `json:"deleted"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	TokenVersion    int64      `json:"-"`
}

type TermsAcceptance struct {
	UserID     snow.ID   `json:"user_id"`
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	AcceptedAt time.Time `json:"accepted_at"`
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

type GroupMember struct {
	UserID snow.ID `json:"user_id"`
	Name   string  `json:"name"`
	Email  string  `json:"email"`
}

// RefreshToken is one refresh session; it holds only SHA-256 hashes of the
// secret.
type RefreshToken struct {
	ID               int64      `json:"id"`
	UserID           snow.ID    `json:"user_id"`
	TokenHash        string     `json:"-"`
	PreviousHash     string     `json:"-"`
	RotatedAt        *time.Time `json:"rotated_at,omitempty"`
	SessionStartedAt *time.Time `json:"session_started_at,omitempty"`
	AuthMethod       string     `json:"auth_method,omitempty"`
	MFA              string     `json:"mfa,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// NewRefreshSession describes a session a login is about to open.
type NewRefreshSession struct {
	UserID     snow.ID
	TokenHash  string
	StartedAt  time.Time
	ExpiresAt  time.Time
	AuthMethod string
	MFA        string
}

// RefreshRotation replaces the secret hash of the session whose current hash
// is MatchHash.
type RefreshRotation struct {
	ID        int64
	MatchHash string
	NewHash   string
	RotatedAt time.Time
	ExpiresAt time.Time
}

type SSHLoginChallenge struct {
	Email     string    `json:"email"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// UserIdentity links one external OAuth account to a nipad user.
type UserIdentity struct {
	ID                snow.ID   `json:"id"`
	UserID            snow.ID   `json:"user_id"`
	Provider          string    `json:"provider"`
	ProviderAccountID string    `json:"provider_account_id"`
	Email             string    `json:"email"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
