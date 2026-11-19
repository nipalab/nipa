package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRepoPath(t *testing.T) {
	for _, p := range []string{"a", "/a/b.txt", "dir/sub/file", "a b/c"} {
		assert.NoError(t, ValidateRepoPath(p), p)
	}
	for _, p := range []string{"", "/", "//a", "a\\b", "a//b", "a/./b", "../a", "a/..", "a/"} {
		err := ValidateRepoPath(p)
		require.Error(t, err, p)
		assert.False(t, IsServerError(err), p)
	}
}

func TestValidateRefNames(t *testing.T) {
	for _, n := range []string{"main", "feature-x", "v1.0.0", "ü"} {
		assert.NoError(t, ValidateBranchName(n), n)
		assert.NoError(t, ValidateTagName(n), n)
	}
	for _, n := range []string{"", ".", "..", "a/b", "a b", "a\tb", "a\x7fb"} {
		assert.Error(t, ValidateBranchName(n), n)
		assert.Error(t, ValidateTagName(n), n)
	}
	assert.EqualError(t, ValidateBranchName(""), "branch name must not be empty")
	assert.EqualError(t, ValidateTagName("a/b"), `invalid tag name "a/b"`)
	assert.True(t, InvalidRefNameRune('/'))
	assert.False(t, InvalidRefNameRune('-'))
}
