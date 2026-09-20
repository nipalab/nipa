package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type Permission int64

const (
	PermissionRead  Permission = 1 << iota // 1
	PermissionWrite                        // 2
	PermissionLock                         // 4
	PermissionAdmin Permission = 1 << 16   // 65536

	PermissionAll Permission = PermissionRead | PermissionWrite | PermissionLock | PermissionAdmin
)

// Has reports whether every bit in perm is set.
func (p Permission) Has(perm Permission) bool {
	return p&perm == perm
}

// PBACRule grants permission on a path prefix to a user or a group.
// projectID is nil for rules that apply to every project in the org.
type PBACRule struct {
	ID         int64      `json:"id"`
	UserID     *snow.ID   `json:"user_id,omitempty"`
	GroupID    *snow.ID   `json:"group_id,omitempty"`
	OrgID      snow.ID    `json:"org_id"`
	ProjectID  *snow.ID   `json:"project_id,omitempty"`
	PathPrefix string     `json:"path_prefix"`
	Permission Permission `json:"permission"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ProjectPathPermission is the default permission for a path prefix,
// applied to users without a matching PBAC rule. A zero Permission is the
// fallback deny.
type ProjectPathPermission struct {
	ID         int64      `json:"id"`
	ProjectID  snow.ID    `json:"project_id"`
	PathPrefix string     `json:"path_prefix"`
	Permission Permission `json:"permission"`
	CreatedAt  time.Time  `json:"created_at"`
}
