package domain

import "io/fs"

// File mode classes shared by the client and the wire protocol. The values
// match the pb.FileMode enum (READ_ONLY=1, READ_WRITE=2, EXECUTABLE=3), which
// is also what the client tracks in its local snapshot.
const (
	FileModeClassReadOnly   = 1
	FileModeClassReadWrite  = 2
	FileModeClassExecutable = 3
)

// ClassifyPerm maps a Unix permission to its file mode class. Only the 0o777
// bits count (setuid/setgid/sticky and type bits are ignored): any execute bit
// makes the file executable; otherwise a file with no write bit is read-only;
// everything else is read-write. Status and push both classify through here so
// a permission can never read as one class locally and land as another.
func ClassifyPerm(perm fs.FileMode) int {
	perm = perm.Perm()
	switch {
	case perm&0o111 != 0:
		return FileModeClassExecutable
	case perm&0o222 == 0:
		return FileModeClassReadOnly
	default:
		return FileModeClassReadWrite
	}
}
