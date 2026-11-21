package domain

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLinkKind(t *testing.T) {
	for mode, want := range map[fs.FileMode]string{
		0o755 | fs.ModeDir:                  "",
		0o644:                               "",
		fs.ModeSymlink:                      "symbolic link",
		fs.ModeSymlink | fs.ModeDir:         "symbolic link",
		fs.ModeIrregular:                    "junction or reparse point",
		fs.ModeIrregular | fs.ModeDir:       "",
		fs.ModeIrregular | fs.ModeDir | 0o1: "",
	} {
		require.Equal(t, want, LinkKind(mode), "%v", mode)
	}
}
