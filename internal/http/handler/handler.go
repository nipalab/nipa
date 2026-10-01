package handler

import (
	"github.com/nipalab/nipa/internal/usecase"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	User() *usecase.User
	Common() *usecase.Common
	Permission() *usecase.Permission
	Org() *usecase.Org
	Group() *usecase.Group
	Project() *usecase.Project
	Branch() *usecase.Branch
	Tag() *usecase.Tag
	MergeRequest() *usecase.MergeRequest
	MergeRequestReview() *usecase.MergeRequestReview
	FileLock() *usecase.FileLock
	Webhook() *usecase.Webhook
}

type Handler struct {
	useCase usecaseContainer
}

func NewHandler(useCase usecaseContainer) *Handler {
	return &Handler{
		useCase: useCase,
	}
}
