package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPermissionHas(t *testing.T) {
	require.True(t, PermissionRead.Has(PermissionRead))
	require.True(t, PermissionAll.Has(PermissionRead))
	require.True(t, PermissionAll.Has(PermissionRead|PermissionWrite))
	require.False(t, PermissionRead.Has(PermissionWrite))
	require.False(t, Permission(0).Has(PermissionRead))
	require.True(t, Permission(0).Has(0))
}

func TestPermission_IsValid(t *testing.T) {
	require.True(t, Permission(0).IsValid())
	require.True(t, PermissionRead.IsValid())
	require.True(t, PermissionAll.IsValid())
	require.False(t, Permission(1<<20).IsValid())
	require.False(t, (PermissionRead | 1<<20).IsValid())
}
