// Package serverapp owns the nipad server lifecycle shared by the free and
// enterprise binaries: the REST API, the gRPC service, the embedded web UI and
// graceful shutdown.
package serverapp

import "github.com/nipalab/nipa/internal/usecase"

// Usecases is the set of business usecases the server exposes. Both the free
// (sqlite) and enterprise (postgres) binaries build it from their own
// repositories.
type Usecases struct {
	Auth               *usecase.Auth
	User               *usecase.User
	Branch             *usecase.Branch
	Tag                *usecase.Tag
	Common             *usecase.Common
	Push               *usecase.Push
	Chunk              *usecase.Chunk
	Permission         *usecase.Permission
	Group              *usecase.Group
	Org                *usecase.Org
	Project            *usecase.Project
	MergeRequest       *usecase.MergeRequest
	MergeRequestReview *usecase.MergeRequestReview
	MergeRequestCheck  *usecase.MergeRequestCheck
	FileLock           *usecase.FileLock
	Webhook            *usecase.Webhook
	EmailDelivery      *usecase.EmailDelivery
}

// Registry adapts the usecase set to the containers expected by the HTTP and
// gRPC server packages.
type Registry struct {
	uc Usecases
}

// NewRegistry wraps the usecase set in a server registry.
func NewRegistry(uc Usecases) *Registry {
	return &Registry{uc: uc}
}

func (r *Registry) Auth() *usecase.Auth {
	return r.uc.Auth
}

func (r *Registry) User() *usecase.User {
	return r.uc.User
}

func (r *Registry) Branch() *usecase.Branch {
	return r.uc.Branch
}

func (r *Registry) Tag() *usecase.Tag {
	return r.uc.Tag
}

func (r *Registry) Common() *usecase.Common {
	return r.uc.Common
}

func (r *Registry) Push() *usecase.Push {
	return r.uc.Push
}

func (r *Registry) Chunk() *usecase.Chunk {
	return r.uc.Chunk
}

func (r *Registry) Permission() *usecase.Permission {
	return r.uc.Permission
}

func (r *Registry) Group() *usecase.Group {
	return r.uc.Group
}

func (r *Registry) Org() *usecase.Org {
	return r.uc.Org
}

func (r *Registry) Project() *usecase.Project {
	return r.uc.Project
}

func (r *Registry) MergeRequest() *usecase.MergeRequest {
	return r.uc.MergeRequest
}

func (r *Registry) MergeRequestReview() *usecase.MergeRequestReview {
	return r.uc.MergeRequestReview
}

func (r *Registry) MergeRequestCheck() *usecase.MergeRequestCheck {
	return r.uc.MergeRequestCheck
}

func (r *Registry) FileLock() *usecase.FileLock {
	return r.uc.FileLock
}

func (r *Registry) Webhook() *usecase.Webhook {
	return r.uc.Webhook
}

func (r *Registry) EmailDelivery() *usecase.EmailDelivery {
	return r.uc.EmailDelivery
}
