package serverapp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/usecase"
)

func TestRegistryExposesUsecases(t *testing.T) {
	uc := Usecases{
		Auth:               &usecase.Auth{},
		User:               &usecase.User{},
		Branch:             &usecase.Branch{},
		Tag:                &usecase.Tag{},
		Common:             &usecase.Common{},
		Push:               &usecase.Push{},
		Chunk:              &usecase.Chunk{},
		Permission:         &usecase.Permission{},
		Group:              &usecase.Group{},
		Org:                &usecase.Org{},
		Project:            &usecase.Project{},
		MergeRequest:       &usecase.MergeRequest{},
		MergeRequestReview: &usecase.MergeRequestReview{},
		MergeRequestCheck:  &usecase.MergeRequestCheck{},
		FileLock:           &usecase.FileLock{},
		Webhook:            &usecase.Webhook{},
	}

	reg := NewRegistry(uc)

	require.Same(t, uc.Auth, reg.Auth())
	require.Same(t, uc.User, reg.User())
	require.Same(t, uc.Branch, reg.Branch())
	require.Same(t, uc.Tag, reg.Tag())
	require.Same(t, uc.Common, reg.Common())
	require.Same(t, uc.Push, reg.Push())
	require.Same(t, uc.Chunk, reg.Chunk())
	require.Same(t, uc.Permission, reg.Permission())
	require.Same(t, uc.Group, reg.Group())
	require.Same(t, uc.Org, reg.Org())
	require.Same(t, uc.Project, reg.Project())
	require.Same(t, uc.MergeRequest, reg.MergeRequest())
	require.Same(t, uc.MergeRequestReview, reg.MergeRequestReview())
	require.Same(t, uc.MergeRequestCheck, reg.MergeRequestCheck())
	require.Same(t, uc.FileLock, reg.FileLock())
	require.Same(t, uc.Webhook, reg.Webhook())
}
