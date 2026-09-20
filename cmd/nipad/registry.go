package main

import "github.com/nipalab/nipa/internal/usecase"

type Registry struct {
	authUsecase       *usecase.Auth
	userUsecase       *usecase.User
	branchUsecase     *usecase.Branch
	commonUsecase     *usecase.Common
	pushUsecase       *usecase.Push
	chunkUsecase      *usecase.Chunk
	permissionUsecase *usecase.Permission
	groupUsecase      *usecase.Group
	orgUsecase        *usecase.Org
}

func (r *Registry) Auth() *usecase.Auth {
	return r.authUsecase
}

func (r *Registry) User() *usecase.User {
	return r.userUsecase
}

func (r *Registry) Branch() *usecase.Branch {
	return r.branchUsecase
}

func (r *Registry) Common() *usecase.Common {
	return r.commonUsecase
}

func (r *Registry) Push() *usecase.Push {
	return r.pushUsecase
}

func (r *Registry) Chunk() *usecase.Chunk {
	return r.chunkUsecase
}

func (r *Registry) Permission() *usecase.Permission {
	return r.permissionUsecase
}

func (r *Registry) Group() *usecase.Group {
	return r.groupUsecase
}

func (r *Registry) Org() *usecase.Org {
	return r.orgUsecase
}
