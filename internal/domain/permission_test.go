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
