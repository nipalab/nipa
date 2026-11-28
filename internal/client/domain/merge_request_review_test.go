package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeRequestDiffFile_CountsLabel(t *testing.T) {
	exact := &MergeRequestDiffFile{Additions: 3, Deletions: 1}
	require.Equal(t, "+3 -1", exact.CountsLabel())

	approx := &MergeRequestDiffFile{Additions: 120, Deletions: 80, CountsApproximate: true}
	require.Equal(t, "+~120 -~80", approx.CountsLabel())
}
