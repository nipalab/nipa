package domain

// PermissionEntry is a path prefix with its permission bitmask.
type PermissionEntry struct {
	PathPrefix string
	Permission uint64
}

// PBACRuleInfo is one server-side rule returned by the admin API.
type PBACRuleInfo struct {
	ID         int64
	UserID     string
	GroupID    string
	PathPrefix string
	Permission uint64
}

// PermissionInfo is the caller's effective permissions on a project.
type PermissionInfo struct {
	ProjectPermission uint64
	Rules             []PermissionEntry
	Defaults          []PermissionEntry
}

// GroupInfo is an organization group.
type GroupInfo struct {
	ID          string
	OrgID       string
	Name        string
	Description string
}
