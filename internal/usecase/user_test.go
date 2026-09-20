package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestNewUser(t *testing.T) {
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	require.NotNil(t, NewUser(node, nil))
}

func TestUser_Get(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockuserRepository(ctrl)
	want := &domain.User{ID: 7, Name: "alice", Email: "alice@example.com"}
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(want, nil)

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	got, err := NewUser(node, repo).Get(context.Background(), snow.ID(7))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestUser_Get_Error(t *testing.T) {
	wantErr := errors.New("db down")
	ctrl := gomock.NewController(t)
	repo := NewMockuserRepository(ctrl)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, wantErr)

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	_, err = NewUser(node, repo).Get(context.Background(), snow.ID(9))
	require.ErrorIs(t, err, wantErr)
}
