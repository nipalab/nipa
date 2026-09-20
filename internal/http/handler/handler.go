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
}

type Handler struct {
	useCase usecaseContainer
}

func NewHandler(useCase usecaseContainer) *Handler {
	return &Handler{
		useCase: useCase,
	}
}
