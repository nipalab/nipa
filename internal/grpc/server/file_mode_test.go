package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nipalab/nipa/internal/grpc/pb"
)

func TestDomainFileModeToPB(t *testing.T) {
	cases := map[int]pb.FileMode{
		0o444: pb.FileMode_FILE_MODE_READ_ONLY,
		444:   pb.FileMode_FILE_MODE_READ_ONLY, // legacy decimal
		0o755: pb.FileMode_FILE_MODE_EXECUTABLE,
		755:   pb.FileMode_FILE_MODE_EXECUTABLE, // legacy decimal
		0o644: pb.FileMode_FILE_MODE_READ_WRITE,
		0:     pb.FileMode_FILE_MODE_READ_WRITE,
		0o775: pb.FileMode_FILE_MODE_EXECUTABLE,
		0o700: pb.FileMode_FILE_MODE_EXECUTABLE,
		0o555: pb.FileMode_FILE_MODE_EXECUTABLE,
		0o400: pb.FileMode_FILE_MODE_READ_ONLY,
		0o600: pb.FileMode_FILE_MODE_READ_WRITE,
		0o666: pb.FileMode_FILE_MODE_READ_WRITE,
	}
	for mode, want := range cases {
		assert.Equal(t, want, domainFileModeToPB(mode), "mode %#o", mode)
	}
}

func TestPBFileModeRoundTrip(t *testing.T) {
	for _, m := range []pb.FileMode{pb.FileMode_FILE_MODE_READ_ONLY, pb.FileMode_FILE_MODE_READ_WRITE, pb.FileMode_FILE_MODE_EXECUTABLE} {
		assert.Equal(t, m, domainFileModeToPB(pbFileModeToInt(m)))
	}
	assert.Equal(t, pb.FileMode_FILE_MODE_READ_WRITE, domainFileModeToPB(pbFileModeToInt(pb.FileMode_FILE_MODE_UNSPECIFIED)))
}
