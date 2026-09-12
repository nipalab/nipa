package main

import "github.com/nipalab/nipa/internal/client/usecase"

type Registry struct {
	authUsecase   *usecase.Auth
	repoUsecase   *usecase.Repo
	pushUsecase   *usecase.Push
	updateUsecase *usecase.Update
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
