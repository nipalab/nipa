package server

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

type fakePushStream struct {
	grpc.ServerStream
	ctx     context.Context
	msgs    []*pb.PushStreamRequest
	recvErr error
	read    int
	resp    *pb.PushResponse
}

func (s *fakePushStream) Context() context.Context { return s.ctx }

func (s *fakePushStream) Recv() (*pb.PushStreamRequest, error) {
	if s.read < len(s.msgs) {
		s.read++
		return s.msgs[s.read-1], nil
	}
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	return nil, io.EOF
}

func (s *fakePushStream) SendAndClose(resp *pb.PushResponse) error {
	s.resp = resp
	return nil
}

func pushCtx() context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
}

func streamHeader(branch, message string) *pb.PushStreamRequest {
	return &pb.PushStreamRequest{Part: &pb.PushStreamRequest_Header{Header: &pb.PushStreamHeader{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  branch,
		Message: message,
	}}}
}

func streamFiles(files ...*pb.PushStreamFile) *pb.PushStreamRequest {
	return &pb.PushStreamRequest{Part: &pb.PushStreamRequest_Files{Files: &pb.PushStreamFiles{Files: files}}}
}

func streamChunks(hashes ...[]byte) *pb.PushStreamRequest {
	return &pb.PushStreamRequest{Part: &pb.PushStreamRequest_Chunks{Chunks: &pb.PushStreamChunks{ChunkHashes: hashes}}}
}

func streamRemoved(paths ...string) *pb.PushStreamRequest {
	return &pb.PushStreamRequest{Part: &pb.PushStreamRequest_Removed{Removed: &pb.PushStreamRemoved{Paths: paths}}}
}

func raw(h domain.Hash) []byte { return append([]byte(nil), h[:]...) }

func runPushStream(t *testing.T, push *usecase.Push, stream *fakePushStream) error {
	t.Helper()
	srv := New(&mockUsecaseContainer{common: newTestCommon(), push: push})
	return srv.PushStream(stream)
}

func TestPushStream_HeaderErrors(t *testing.T) {
	cases := map[string][]*pb.PushStreamRequest{
		"empty stream":      nil,
		"no header first":   {streamRemoved("a")},
		"no commit message": {streamHeader("main", "")},
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			push, _, _, _ := newPushUsecase(t)
			err := runPushStream(t, push, &fakePushStream{ctx: pushCtx(), msgs: msgs})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestPushStream_FirstRecvError(t *testing.T) {
	push, _, _, _ := newPushUsecase(t)
	cancelled := status.Error(codes.Canceled, "client went away")
	err := runPushStream(t, push, &fakePushStream{ctx: pushCtx(), recvErr: cancelled})
	require.Equal(t, cancelled, err)
}

func TestPushStream_RejectsBeforeReadingFiles(t *testing.T) {
	head := snow.ID(9)
	cases := []struct {
		name   string
		setup  func(perm *MockpermissionUsecase, repo *MockbranchRepository)
		header func() *pb.PushStreamRequest
		code   codes.Code
	}{
		{
			name: "no write access",
			setup: func(perm *MockpermissionUsecase, _ *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(false)
			},
			header: func() *pb.PushStreamRequest { return streamHeader("main", "msg") },
			code:   codes.PermissionDenied,
		},
		{
			name: "missing branch",
			setup: func(perm *MockpermissionUsecase, repo *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
				repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "nope").Return(nil, domain.NewErrorNotFound("x"))
			},
			header: func() *pb.PushStreamRequest { return streamHeader("nope", "msg") },
			code:   codes.NotFound,
		},
		{
			name: "protected branch",
			setup: func(perm *MockpermissionUsecase, repo *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
				repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
					Return(&domain.Branch{ID: 5, Name: "main", IsProtected: true}, nil)
			},
			header: func() *pb.PushStreamRequest { return streamHeader("main", "msg") },
			code:   codes.PermissionDenied,
		},
		{
			name: "branch moved",
			setup: func(perm *MockpermissionUsecase, repo *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
				repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
					Return(&domain.Branch{ID: 5, Name: "main", CommitID: &head}, nil)
			},
			header: func() *pb.PushStreamRequest {
				h := streamHeader("main", "msg")
				stale := snow.ID(3).Base36()
				h.GetHeader().BaseCommitId = &stale
				return h
			},
			code: codes.FailedPrecondition,
		},
		{
			name: "invalid base commit",
			setup: func(perm *MockpermissionUsecase, repo *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
				repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
					Return(&domain.Branch{ID: 5, Name: "main"}, nil)
			},
			header: func() *pb.PushStreamRequest {
				h := streamHeader("main", "msg")
				bad := "!!"
				h.GetHeader().BaseCommitId = &bad
				return h
			},
			code: codes.InvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			push, perm, repo, _ := newPushUsecase(t)
			tc.setup(perm, repo)
			stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{tc.header(), streamRemoved("a")}}
			err := runPushStream(t, push, stream)
			require.Equal(t, tc.code, status.Code(err), err)
			require.Equal(t, 1, stream.read, "the file list must not be read")
		})
	}
}

func expectWritableMain(perm *MockpermissionUsecase, repo *MockbranchRepository) {
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true).Times(2)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 42, Name: "main"}, nil).Times(2)
}

func TestPushStream_Success(t *testing.T) {
	push, perm, repo, pushRepo := newPushUsecase(t)
	expectWritableMain(perm, repo)

	a := chunker.Sum([]byte("a"))
	b1, b2, b3 := chunker.Sum([]byte("b1")), chunker.Sum([]byte("b2")), chunker.Sum([]byte("b3"))
	var got usecase.ApplyPushRequest
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req usecase.ApplyPushRequest) error {
			got = req
			return nil
		})

	stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{
		streamHeader("main", "add files"),
		streamFiles(&pb.PushStreamFile{
			Path: "a.txt", Mode: pb.FileMode_FILE_MODE_EXECUTABLE, SizeBytes: 1,
			FileHash: raw(treehash.FileHash([]domain.Hash{a})), ChunkHashes: [][]byte{raw(a)},
		}),
		streamFiles(&pb.PushStreamFile{
			Path: "big.bin", SizeBytes: 3, IsBinary: true,
			FileHash: raw(treehash.FileHash([]domain.Hash{b1, b2, b3})), ChunkHashes: [][]byte{raw(b1)},
		}),
		streamChunks(raw(b2)),
		streamChunks(raw(b3)),
	}}
	require.NoError(t, runPushStream(t, push, stream))
	require.NotNil(t, stream.resp)
	require.NotEmpty(t, stream.resp.GetCommitId())
	require.Len(t, stream.resp.GetCommitHash(), 64)

	files := map[string]usecase.PushFileRow{}
	for _, f := range got.Files {
		files[f.Name] = f
	}
	require.Equal(t, []domain.Hash{a}, files["a.txt"].ChunkHashes)
	require.Equal(t, 0o755, files["a.txt"].Mode)
	require.Equal(t, []domain.Hash{b1, b2, b3}, files["big.bin"].ChunkHashes)
	require.True(t, files["big.bin"].IsBinary)
	require.Equal(t, "add files", got.Message)
}

func TestPushStream_PartErrors(t *testing.T) {
	good := chunker.Sum([]byte("x"))
	cases := map[string]*pb.PushStreamRequest{
		"second header":        streamHeader("main", "again"),
		"chunks before a file": streamChunks(raw(good)),
		"empty part":           {},
		"short file hash":      streamFiles(&pb.PushStreamFile{Path: "a", FileHash: []byte{1}}),
		"short chunk hash":     streamFiles(&pb.PushStreamFile{Path: "a", FileHash: raw(good), ChunkHashes: [][]byte{{1}}}),
	}
	for name, part := range cases {
		t.Run(name, func(t *testing.T) {
			push, perm, repo, _ := newPushUsecase(t)
			perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
			repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 5, Name: "main"}, nil)
			stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{streamHeader("main", "msg"), part}}
			require.Equal(t, codes.InvalidArgument, status.Code(runPushStream(t, push, stream)))
		})
	}
}

func TestPushStream_InvalidContinuationHash(t *testing.T) {
	push, perm, repo, _ := newPushUsecase(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 5, Name: "main"}, nil)
	good := raw(chunker.Sum([]byte("x")))
	stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{
		streamHeader("main", "msg"),
		streamFiles(&pb.PushStreamFile{Path: "a", FileHash: good, ChunkHashes: [][]byte{good}}),
		streamChunks([]byte("short")),
	}}
	err := runPushStream(t, push, stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "invalid chunk hash for a")
}

func TestPushStream_TooManyFiles(t *testing.T) {
	push, perm, repo, _ := newPushUsecase(t)
	push.WithMaxFiles(2)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 5, Name: "main"}, nil)
	stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{
		streamHeader("main", "msg"),
		streamRemoved("a", "b"),
		streamRemoved("c"),
		streamRemoved("d"),
	}}
	err := runPushStream(t, push, stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "more than the server limit of 2")
	require.Equal(t, 3, stream.read)
}

func TestPushStream_RecvErrorMidStream(t *testing.T) {
	push, perm, repo, _ := newPushUsecase(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 5, Name: "main"}, nil)
	cancelled := status.Error(codes.Canceled, "client went away")
	stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{streamHeader("main", "msg"), streamRemoved("a")}, recvErr: cancelled}
	require.Equal(t, cancelled, runPushStream(t, push, stream))
	require.Nil(t, stream.resp)
}

func TestPushStream_UsecaseRejectsManifest(t *testing.T) {
	push, perm, repo, _ := newPushUsecase(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true).Times(2)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 5, Name: "main"}, nil)
	h := chunker.Sum([]byte("x"))
	stream := &fakePushStream{ctx: pushCtx(), msgs: []*pb.PushStreamRequest{
		streamHeader("main", "msg"),
		streamFiles(&pb.PushStreamFile{Path: "a", SizeBytes: 1, FileHash: raw(domain.Hash{9}), ChunkHashes: [][]byte{raw(h)}}),
	}}
	err := runPushStream(t, push, stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "file hash mismatch")
}

func TestPushReceiver_ByteLimit(t *testing.T) {
	r := pushReceiver{maxFiles: 100, maxBytes: 64, tooMany: func(int) error { return errors.New("unused") }}
	require.NoError(t, r.add(streamRemoved("a")))
	err := r.add(streamRemoved("a-very-long-path/that/pushes/the/manifest/over/its/byte/limit.txt"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "larger than the server limit of 64 bytes")
}

func TestPushUsecase_MaxFiles(t *testing.T) {
	push, perm, _, _ := newPushUsecase(t)
	require.Equal(t, usecase.DefaultPushMaxFiles, push.MaxFiles())
	push.WithMaxFiles(1)
	require.Equal(t, 1, push.MaxFiles())

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	_, err := push.Push(pushCtx(), snow.ID(42), "main", "", "msg", nil, []string{"a", "b"}, "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "more than the server limit of 1")
}
