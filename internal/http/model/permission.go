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
