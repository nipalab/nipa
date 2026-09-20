package main

import "github.com/nipalab/nipa/internal/client/usecase"

type Registry struct {
	authUsecase       *usecase.Auth
	repoUsecase       *usecase.Repo
	pushUsecase       *usecase.Push
	updateUsecase     *usecase.Update
	mergeUsecase      *usecase.Merge
	revertUsecase     *usecase.Revert
	diffUsecase       *usecase.Diff
	permissionUsecase *usecase.Permission
}

func (r *Registry) Auth() *usecase.Auth {
	return r.authUsecase
}

func (r *Registry) Repo() *usecase.Repo {
	return r.repoUsecase
}

func (r *Registry) Push() *usecase.Push {
	return r.pushUsecase
}

func (r *Registry) Update() *usecase.Update {
	return r.updateUsecase
}

func (r *Registry) Merge() *usecase.Merge {
	return r.mergeUsecase
}

func (r *Registry) Revert() *usecase.Revert {
	return r.revertUsecase
}

func (r *Registry) Diff() *usecase.Diff {
	return r.diffUsecase
}

func (r *Registry) Permission() *usecase.Permission {
	return r.permissionUsecase
}
