package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type User struct {
	snowNode snow.Node
	repo     userRepository
}

func NewUser(snowNode snow.Node, repo userRepository) *User {
	return &User{
		snowNode: snowNode,
		repo:     repo,
	}
}

func (u *User) Get(ctx context.Context, id snow.ID) (*domain.User, error) {
	return u.repo.GetByID(ctx, id)
}
