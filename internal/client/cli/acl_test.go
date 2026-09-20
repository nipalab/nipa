package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePermission(t *testing.T) {
	got, err := parsePermission("read,write")
	require.NoError(t, err)
	require.Equal(t, permissionRead|permissionWrite, got)

	got, err = parsePermission(" ADMIN ")
	require.NoError(t, err)
	require.Equal(t, permissionAdmin, got)

	got, err = parsePermission("read,lock,write")
	require.NoError(t, err)
	require.Equal(t, permissionRead|permissionWrite|permissionLock, got)

	_, err = parsePermission("")
	require.Error(t, err)

	_, err = parsePermission("read,execute")
	require.Error(t, err)
}

func TestFormatPermission(t *testing.T) {
	require.Equal(t, "read,write", formatPermission(permissionRead|permissionWrite))
	require.Equal(t, "none", formatPermission(0))
	require.Equal(t, "lock", formatPermission(permissionLock))
	require.Equal(t, "read,write,lock,admin", formatPermission(permissionRead|permissionWrite|permissionLock|permissionAdmin))
}
