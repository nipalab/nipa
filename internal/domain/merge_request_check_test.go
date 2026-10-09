package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsValidMergeRequestCheckState(t *testing.T) {
	require.True(t, IsValidMergeRequestCheckState(MergeRequestCheckPending))
	require.True(t, IsValidMergeRequestCheckState(MergeRequestCheckSuccess))
	require.True(t, IsValidMergeRequestCheckState(MergeRequestCheckFailed))
	require.False(t, IsValidMergeRequestCheckState("exploded"))
	require.False(t, IsValidMergeRequestCheckState(""))
}
