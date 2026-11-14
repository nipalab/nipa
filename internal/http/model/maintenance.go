package model

import "time"

// MaintenanceWindowResponse is one maintenance window. Org is set for org
// and project windows, Project ("org/project") for project windows, Notified
// only in answers to a write.
type MaintenanceWindowResponse struct {
	Mode     string     `json:"mode"`
	Active   bool       `json:"active"`
	Message  string     `json:"message"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Org      string     `json:"org,omitempty"`
	Project  string     `json:"project,omitempty"`
	Notified *bool      `json:"notified,omitempty"`
}

type MaintenanceStatusResponse struct {
	Server *MaintenanceWindowResponse `json:"server"`
	Source string                     `json:"source"`
}

type AdminMaintenanceResponse struct {
	Server   *MaintenanceWindowResponse  `json:"server"`
	Source   string                      `json:"source"`
	Orgs     []MaintenanceWindowResponse `json:"orgs"`
	Projects []MaintenanceWindowResponse `json:"projects"`
}

// OrgMaintenanceResponse lists the org's project windows to org owners only.
type OrgMaintenanceResponse struct {
	Server   *MaintenanceWindowResponse  `json:"server"`
	Org      *MaintenanceWindowResponse  `json:"org"`
	Projects []MaintenanceWindowResponse `json:"projects,omitempty"`
}

type ProjectMaintenanceResponse struct {
	Server  *MaintenanceWindowResponse `json:"server"`
	Org     *MaintenanceWindowResponse `json:"org"`
	Project *MaintenanceWindowResponse `json:"project"`
}

// SetMaintenanceRequest stores a window: mode read_only, full or frozen
// (server only). A missing starts_at keeps the start of an existing window,
// else it means now. ends_at is an estimate and never ends the window.
type SetMaintenanceRequest struct {
	Mode     string     `json:"mode"`
	Message  string     `json:"message"`
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Notify   bool       `json:"notify"`
}
