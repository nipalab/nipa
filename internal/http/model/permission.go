package model

type PermissionEntry struct {
	PathPrefix string `json:"path_prefix"`
	Permission uint64 `json:"permission"`
}

type ProjectPermissionResponse struct {
	ProjectPermission uint64            `json:"project_permission"`
	Rules             []PermissionEntry `json:"rules"`
	Defaults          []PermissionEntry `json:"defaults"`
}

type PBACRuleResponse struct {
	ID         int64  `json:"id"`
	UserID     string `json:"user_id,omitempty"`
	GroupID    string `json:"group_id,omitempty"`
	PathPrefix string `json:"path_prefix"`
	Permission uint64 `json:"permission"`
}

type CreatePBACRuleRequest struct {
	UserID     string `json:"user_id"`
	GroupID    string `json:"group_id"`
	PathPrefix string `json:"path_prefix"`
	Permission uint64 `json:"permission"`
}

type SetPathPermissionRequest struct {
	PathPrefix string `json:"path_prefix"`
	Permission uint64 `json:"permission"`
}
