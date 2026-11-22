package grpc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

func TestIntToPBFileModeFullSpace(t *testing.T) {
	for perm := 0; perm <= 0o777; perm++ {
		want := pb.FileMode_FILE_MODE_READ_WRITE
		switch {
		case perm&0o111 != 0:
			want = pb.FileMode_FILE_MODE_EXECUTABLE
		case perm&0o222 == 0:
			want = pb.FileMode_FILE_MODE_READ_ONLY
		}
		assert.Equal(t, want, intToPBFileMode(perm), "perm %#o", perm)
		// Raw Unix setuid/setgid/sticky bits never change the class.
		for _, special := range []int{0o4000, 0o2000, 0o1000, 0o7000} {
			assert.Equal(t, want, intToPBFileMode(perm|special), "perm %#o", perm|special)
		}
	}
}

func TestIntToPBFileModeExplicit(t *testing.T) {
	cases := map[int]pb.FileMode{
		0o775:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o755:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o700:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o711:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o744:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o555:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o500:  pb.FileMode_FILE_MODE_EXECUTABLE,
		0o4755: pb.FileMode_FILE_MODE_EXECUTABLE,
		0o444:  pb.FileMode_FILE_MODE_READ_ONLY,
		0o400:  pb.FileMode_FILE_MODE_READ_ONLY,
		0o000:  pb.FileMode_FILE_MODE_READ_ONLY,
		0o1444: pb.FileMode_FILE_MODE_READ_ONLY,
		0o644:  pb.FileMode_FILE_MODE_READ_WRITE,
		0o600:  pb.FileMode_FILE_MODE_READ_WRITE,
		0o666:  pb.FileMode_FILE_MODE_READ_WRITE,
		0o2664: pb.FileMode_FILE_MODE_READ_WRITE,
	}
	for perm, want := range cases {
		assert.Equal(t, want, intToPBFileMode(perm), "perm %#o", perm)
	}
}

func TestToPushStreamFileClassifiesMode(t *testing.T) {
	pf := toPushStreamFile(&serverDomain.PushFile{Path: "run.sh", Mode: 0o775})
	assert.Equal(t, pb.FileMode_FILE_MODE_EXECUTABLE, pf.GetMode())
}
