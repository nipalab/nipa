package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubInvalidator struct {
	calls int
}

func (s *stubInvalidator) InvalidateAll() {
	s.calls++
}

func newTestGroup(t *testing.T) (*Group, *MockgroupRepository, *stubInvalidator) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockgroupRepository(ctrl)
	invalidator := &stubInvalidator{}
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewGroup(repo, node, invalidator), repo, invalidator
}

func TestGroup_Create_NoPermission(t *testing.T) {
	group, _, _ := newTestGroup(t)

	_, err := group.Create(permissionCtx(42), 1, "artists", "")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestGroup_Create_Success(t *testing.T) {
	group, repo, _ := newTestGroup(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.Group) (*domain.Group, error) {
			require.NotZero(t, created.ID)
			require.Equal(t, snow.ID(1), created.OrgID)
			require.Equal(t, "artists", created.Name)
			require.Equal(t, "2d team", created.Description)
			return &created, nil
		},
	)

	got, err := group.Create(ctx, 1, "  artists  ", " 2d team ")
	require.NoError(t, err)
	require.Equal(t, "artists", got.Name)
}

func TestGroup_Create_EmptyName(t *testing.T) {
	group, _, _ := newTestGroup(t)

	_, err := group.Create(permissionCtx(42, withAdmin()), 1, "   ", "")
	requireUserError(t, err)
}

func TestGroup_List_NoPermission(t *testing.T) {
	group, _, _ := newTestGroup(t)

	_, err := group.List(permissionCtx(42), 1)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestGroup_List_Success(t *testing.T) {
	group, repo, _ := newTestGroup(t)
	ctx := permissionCtx(42, withAdmin())

	want := []*domain.Group{{ID: 7, OrgID: 1, Name: "artists"}}
	repo.EXPECT().ListByOrg(gomock.Any(), snow.ID(1)).Return(want, nil)

	got, err := group.List(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestGroup_AddMember_NoPermission(t *testing.T) {
	group, _, _ := newTestGroup(t)

	err := group.AddMember(permissionCtx(42), 1, 7, 9)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestGroup_AddMember_GroupNotFound(t *testing.T) {
	group, repo, _ := newTestGroup(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(nil, domain.NewErrorRecordNotFound())

	err := group.AddMember(ctx, 1, 7, 9)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestGroup_AddMember_WrongOrg(t *testing.T) {
	group, repo, _ := newTestGroup(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.Group{ID: 7, OrgID: 2}, nil)

	err := group.AddMember(ctx, 1, 7, 9)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestGroup_AddMember_SuccessInvalidatesCache(t *testing.T) {
	group, repo, invalidator := newTestGroup(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.Group{ID: 7, OrgID: 1}, nil)
	repo.EXPECT().AddMember(gomock.Any(), snow.ID(7), snow.ID(9)).Return(nil)

	require.NoError(t, group.AddMember(ctx, 1, 7, 9))
	require.Equal(t, 1, invalidator.calls)
}

func TestGroup_RemoveMember_SuccessInvalidatesCache(t *testing.T) {
	group, repo, invalidator := newTestGroup(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.Group{ID: 7, OrgID: 1}, nil)
	repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(7), snow.ID(9)).Return(nil)

	require.NoError(t, group.RemoveMember(ctx, 1, 7, 9))
	require.Equal(t, 1, invalidator.calls)
}
