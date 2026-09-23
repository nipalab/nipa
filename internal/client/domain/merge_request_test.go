package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsValidMergeRequestStatus(t *testing.T) {
	require.True(t, IsValidMergeRequestStatus(MergeRequestOpen))
	require.True(t, IsValidMergeRequestStatus(MergeRequestMerged))
	require.True(t, IsValidMergeRequestStatus(MergeRequestClosed))
	require.False(t, IsValidMergeRequestStatus("bogus"))
	require.False(t, IsValidMergeRequestStatus(""))
}
