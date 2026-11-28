package domain

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRepoPath(t *testing.T) {
	for _, p := range []string{"a", "a/b.txt", "dir/sub/file", "a b/c"} {
		assert.NoError(t, ValidateRepoPath(p), p)
	}
	for _, p := range []string{"", "/", "//a", "/a", "/a/b.txt", "a\\b", "a//b", "a/./b", "../a", "a/..", "a/", "a\x00b"} {
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

func TestValidateRepoPath_RejectsMetadataDir(t *testing.T) {
	for _, p := range []string{".nipa/config", ".NIPA/config", "a/.Nipa/b", "a/.nipa", ".nipa./x", "NIPA~1/config", "a\x00b"} {
		err := ValidateRepoPath(p)
		require.Error(t, err, p)
		assert.False(t, IsServerError(err), p)
	}
	assert.NoError(t, ValidateRepoPath(".nipaignore"))
	assert.NoError(t, ValidateRepoPath("a/.nipa-x/b"))
	assert.Contains(t, ValidateRepoPath("a/.NIPA/b").Error(), "reserved for clone metadata")
}

func TestValidateRemovedRepoPath(t *testing.T) {
	assert.NoError(t, ValidateRemovedRepoPath(".nipa/config"))
	assert.NoError(t, ValidateRemovedRepoPath("a/b"))
	for _, p := range []string{"", "../a", "a//b", "a\\b"} {
		assert.Error(t, ValidateRemovedRepoPath(p), p)
	}
}

func TestValidatePathSegment(t *testing.T) {
	valid := []string{"a", "file.txt", "a b", ".nipaignore", "...", "aux.c", "con", "a:b", "trailing.", "ü", "nipa~2"}
	for _, n := range valid {
		assert.NoError(t, ValidatePathSegment(n), n)
	}
	cases := map[string]string{
		"":                "empty name",
		".":               "relative path segment",
		"..":              "relative path segment",
		"a/b":             "contains a path separator",
		"../x":            "contains a path separator",
		"a\\b":            "contains a path separator",
		"a\x00b":          "contains a NUL byte",
		".nipa":           "reserved for clone metadata",
		".NIPA":           "reserved for clone metadata",
		".nIpA":           "reserved for clone metadata",
		".nipa.":          "reserved for clone metadata",
		".nipa ":          "reserved for clone metadata",
		".ni\u200cpa":     "reserved for clone metadata",
		"\ufeff.nipa":     "reserved for clone metadata",
		"NIPA~1":          "reserved for clone metadata",
		"nipa~1. ":        "reserved for clone metadata",
		".nipa\u202e. . ": "reserved for clone metadata",
	}
	for n, want := range cases {
		err := ValidatePathSegment(n)
		require.Error(t, err, "%q", n)
		assert.Contains(t, err.Error(), want, "%q", n)
		assert.False(t, IsServerError(err), "%q", n)
	}
}

func TestValidatePortablePathSegment(t *testing.T) {
	for _, n := range []string{"a", "file.txt", "console.log", "com10", "lpt0", "auxiliary", "CONx.txt", ".hidden"} {
		assert.NoError(t, ValidatePortablePathSegment(n), n)
	}
	cases := map[string]string{
		"..":         "relative path segment",
		"a:b":        "contains ':'",
		"c:":         "contains ':'",
		"x::$DATA":   "contains ':'",
		"trailing.":  "ends with a dot or space",
		"trailing ":  "ends with a dot or space",
		"CON":        "reserved Windows device name",
		"con":        "reserved Windows device name",
		"aux.c":      "reserved Windows device name",
		"Nul.tar.gz": "reserved Windows device name",
		"PRN .txt":   "reserved Windows device name",
		"COM1":       "reserved Windows device name",
		"lpt9.log":   "reserved Windows device name",
		"COM\u00b9":  "reserved Windows device name",
		"CONOUT$":    "reserved Windows device name",
	}
	for n, want := range cases {
		err := ValidatePortablePathSegment(n)
		require.Error(t, err, "%q", n)
		assert.Contains(t, err.Error(), want, "%q", n)
	}
}

func TestValidateSegmentForOS(t *testing.T) {
	assert.NoError(t, validateSegmentFor("linux", "aux.c"))
	assert.NoError(t, validateSegmentFor("darwin", "a:b"))
	assert.Error(t, validateSegmentFor("windows", "aux.c"))
	assert.Error(t, validateSegmentFor("linux", ".nipa"))
	assert.Equal(t, validateSegmentFor(runtime.GOOS, "a:b") == nil, ValidateLocalPathSegment("a:b") == nil)
}

func TestValidateLocalPath(t *testing.T) {
	assert.NoError(t, ValidateLocalPath("dir/sub/file.txt"))
	for _, p := range []string{"", "../x", "a/../x", "a//b", ".nipa/config", "a/.NIPA/b", "a/b\\c"} {
		err := ValidateLocalPath(p)
		require.Error(t, err, p)
		assert.False(t, IsServerError(err), p)
	}
	assert.EqualError(t, ValidateLocalPath("a/../x"), `invalid file path "a/../x": invalid name "..": relative path segment`)
}
