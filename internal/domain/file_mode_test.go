package domain

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyPermFullSpace(t *testing.T) {
	for perm := fs.FileMode(0); perm <= 0o777; perm++ {
		want := FileModeClassReadWrite
		switch {
		case perm&0o111 != 0:
			want = FileModeClassExecutable
		case perm&0o222 == 0:
			want = FileModeClassReadOnly
		}
		assert.Equal(t, want, ClassifyPerm(perm), "perm %#o", uint32(perm))
		for _, special := range []fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky, fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky} {
			assert.Equal(t, want, ClassifyPerm(perm|special), "perm %#o with %v", uint32(perm), special)
		}
	}
}

func TestClassifyPermExplicit(t *testing.T) {
	cases := []struct {
		perm fs.FileMode
		want int
	}{
		{0o775, FileModeClassExecutable},
		{0o755, FileModeClassExecutable},
		{0o700, FileModeClassExecutable},
		{0o711, FileModeClassExecutable},
		{0o744, FileModeClassExecutable},
		{0o555, FileModeClassExecutable},
		{0o500, FileModeClassExecutable},
		{0o001, FileModeClassExecutable},
		{0o444, FileModeClassReadOnly},
		{0o400, FileModeClassReadOnly},
		{0o000, FileModeClassReadOnly},
		{0o644, FileModeClassReadWrite},
		{0o600, FileModeClassReadWrite},
		{0o666, FileModeClassReadWrite},
		{0o200, FileModeClassReadWrite},
		{0o664 | fs.ModeSetgid, FileModeClassReadWrite},
		{0o755 | fs.ModeSetuid, FileModeClassExecutable},
		{0o444 | fs.ModeSticky, FileModeClassReadOnly},
		{0o644 | fs.ModeDir, FileModeClassReadWrite},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, ClassifyPerm(tc.perm), "perm %v", tc.perm)
	}
}
