package server

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

type importDeps struct {
	perm     *MockpermissionUsecase
	branches *MockbranchRepository
	push     *MockpushRepository
	tags     *MocktagRepository
	users    *MockimportUsers
	orgs     *MockimportOrgMembers
	projects *MockimportProjectOrgs
	locker   *MockimportLocker
	quota    *MockimportQuota
}

type passTx struct{}

func (passTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func newImportServer(t *testing.T) (*nipaServer, *importDeps) {
	t.Helper()
	ctrl := gomock.NewController(t)
	d := &importDeps{
		perm:     NewMockpermissionUsecase(ctrl),
		branches: NewMockbranchRepository(ctrl),
		push:     NewMockpushRepository(ctrl),
		tags:     NewMocktagRepository(ctrl),
		users:    NewMockimportUsers(ctrl),
		orgs:     NewMockimportOrgMembers(ctrl),
		projects: NewMockimportProjectOrgs(ctrl),
		locker:   NewMockimportLocker(ctrl),
		quota:    NewMockimportQuota(ctrl),
	}
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	uc := usecase.NewImport(d.perm, d.branches, d.push, d.tags, d.users, d.orgs, d.projects, d.locker, passTx{}, node).WithQuota(d.quota)
	return New(&mockUsecaseContainer{common: newTestCommon(), importer: uc}), d
}

func (d *importDeps) admin(ok bool) {
	d.perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(42)).Return(ok).AnyTimes()
	d.perm.EXPECT().HasPathAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true).AnyTimes()
}

func origin(c byte) string { return "git:default:" + strings.Repeat(string(c), 40) }

func projectCtx() *pb.ProjectContext { return &pb.ProjectContext{Org: "org", Project: "proj"} }

func TestImportLookupHandler(t *testing.T) {
	t.Run("returns known origins in request order", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(true)
		d.branches.EXPECT().CommitsByOrigins(gomock.Any(), snow.ID(42), []string{origin('b'), origin('c'), origin('a')}).Return([]*domain.Commit{
			{ID: 1, ProjectID: 42, Origin: origin('a'), Hash: domain.Hash{1}},
			{ID: 2, ProjectID: 42, Origin: origin('b'), Hash: domain.Hash{2}},
		}, nil)
		res, err := srv.ImportLookup(pushCtx(), &pb.ImportLookupRequest{Context: projectCtx(), Origins: []string{origin('b'), origin('c'), origin('a')}})
		require.NoError(t, err)
		require.Len(t, res.GetCommits(), 2)
		require.Equal(t, origin('b'), res.GetCommits()[0].GetOrigin())
		require.Equal(t, snow.ID(2).Base36(), res.GetCommits()[0].GetCommitId())
		require.Equal(t, domain.Hash{2}.String(), res.GetCommits()[0].GetCommitHash())
		require.False(t, res.GetQuotaChecked())
	})
	t.Run("checks the upload estimate", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(true)
		d.quota.EXPECT().CheckUploadHeadroom(gomock.Any(), gomock.Any(), gomock.Any(), int64(500)).
			DoAndReturn(func(_ context.Context, org *domain.Organization, project *domain.Project, _ int64) (bool, error) {
				require.Equal(t, snow.ID(1), org.ID)
				require.Equal(t, snow.ID(42), project.ID)
				return true, nil
			})
		res, err := srv.ImportLookup(pushCtx(), &pb.ImportLookupRequest{Context: projectCtx(), EstimatedUploadBytes: 500})
		require.NoError(t, err)
		require.Empty(t, res.GetCommits())
		require.True(t, res.GetQuotaChecked())
	})
	t.Run("estimate over the quota", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(true)
		d.quota.EXPECT().CheckUploadHeadroom(gomock.Any(), gomock.Any(), gomock.Any(), int64(500)).
			Return(true, domain.NewErrorQuotaExceeded("storage quota exceeded"))
		_, err := srv.ImportLookup(pushCtx(), &pb.ImportLookupRequest{Context: projectCtx(), EstimatedUploadBytes: 500})
		require.Equal(t, codes.ResourceExhausted, status.Code(err))
		require.Contains(t, status.Convert(err).Message(), "storage quota exceeded")
	})
	t.Run("unknown project", func(t *testing.T) {
		srv, _ := newImportServer(t)
		_, err := srv.ImportLookup(pushCtx(), &pb.ImportLookupRequest{Context: &pb.ProjectContext{Org: "org", Project: "nope"}})
		require.Error(t, err)
	})
	t.Run("not admin", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(false)
		_, err := srv.ImportLookup(pushCtx(), &pb.ImportLookupRequest{Context: projectCtx(), EstimatedUploadBytes: 500})
		require.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}

func TestImportSetRefsHandler(t *testing.T) {
	t.Run("maps results", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(true)
		d.branches.EXPECT().CommitsByOrigins(gomock.Any(), snow.ID(42), []string{origin('a')}).
			Return([]*domain.Commit{{ID: 5, ProjectID: 42, Origin: origin('a')}}, nil)
		d.locker.EXPECT().LockProject(gomock.Any(), snow.ID(42)).Return(nil)
		d.branches.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 3, Name: "main", CommitID: ptrSnow(5)}, nil)
		d.branches.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "gone").Return(nil, domain.NewErrorNotFound("x"))
		res, err := srv.ImportSetRefs(pushCtx(), &pb.ImportSetRefsRequest{
			Context:       projectCtx(),
			Branches:      []*pb.ImportRef{{Name: "main", Origin: origin('a')}},
			DefaultBranch: "gone",
			Update:        true,
			CommitCount:   3,
		})
		require.NoError(t, err)
		require.Len(t, res.GetRefs(), 2)
		require.Equal(t, &pb.ImportRefResult{Kind: "branch", Name: "main", Status: "unchanged", CommitId: snow.ID(5).Base36()}, res.GetRefs()[0])
		require.Equal(t, "default", res.GetRefs()[1].GetKind())
		require.Empty(t, res.GetRefs()[1].GetCommitId())
		require.NotEmpty(t, res.GetRefs()[1].GetMessage())
	})
	t.Run("not admin", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(false)
		_, err := srv.ImportSetRefs(pushCtx(), &pb.ImportSetRefsRequest{Context: projectCtx()})
		require.Equal(t, codes.PermissionDenied, status.Code(err))
	})
	t.Run("unknown project", func(t *testing.T) {
		srv, _ := newImportServer(t)
		_, err := srv.ImportSetRefs(pushCtx(), &pb.ImportSetRefsRequest{Context: &pb.ProjectContext{Org: "nope"}})
		require.Error(t, err)
	})
}

type fakeImportStream struct {
	grpc.ServerStream
	ctx     context.Context
	msgs    []*pb.ImportStreamRequest
	recvErr error
	sendErr error
	read    int
	sent    []*pb.ImportStreamResponse
}

func (s *fakeImportStream) Context() context.Context { return s.ctx }

func (s *fakeImportStream) Recv() (*pb.ImportStreamRequest, error) {
	if s.read < len(s.msgs) {
		s.read++
		return s.msgs[s.read-1], nil
	}
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	return nil, io.EOF
}

func (s *fakeImportStream) Send(resp *pb.ImportStreamResponse) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, resp)
	return nil
}

func importHeader(h *pb.ImportCommitHeader) *pb.ImportStreamRequest {
	return &pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Header{Header: h}}
}

func importEnd() *pb.ImportStreamRequest {
	return &pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_End{End: &pb.ImportCommitEnd{}}}
}

func importFiles(files ...*pb.PushStreamFile) *pb.ImportStreamRequest {
	return &pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Files{Files: &pb.PushStreamFiles{Files: files}}}
}

func importChunks(hashes ...[]byte) *pb.ImportStreamRequest {
	return &pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Chunks{Chunks: &pb.PushStreamChunks{ChunkHashes: hashes}}}
}

func importRemoved(paths ...string) *pb.ImportStreamRequest {
	return &pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Removed{Removed: &pb.PushStreamRemoved{Paths: paths}}}
}

func TestImportStream_AppliesEachCommit(t *testing.T) {
	srv, d := newImportServer(t)
	d.admin(true)
	parent := &domain.Commit{ID: 8, ProjectID: 42, Origin: origin('b'), Hash: domain.Hash{8}, TreeID: 80}
	gomock.InOrder(
		d.branches.EXPECT().CommitsByOrigins(gomock.Any(), snow.ID(42), []string{origin('a'), origin('b')}).Return([]*domain.Commit{parent}, nil),
		d.branches.EXPECT().CommitsByOrigins(gomock.Any(), snow.ID(42), []string{origin('a')}).
			Return([]*domain.Commit{{ID: 1, ProjectID: 42, Origin: origin('a')}}, nil),
	)
	d.branches.EXPECT().GetTreeNode(gomock.Any(), int64(80)).Return(&domain.TreeNode{ID: 80}, nil)
	d.branches.EXPECT().ListFilesByTree(gomock.Any(), int64(80)).Return(nil, nil).AnyTimes()
	d.branches.EXPECT().ListTreeChildren(gomock.Any(), int64(80)).Return(nil, nil).AnyTimes()
	d.users.EXPECT().GetByID(gomock.Any(), snow.ID(11)).Return(&domain.User{ID: 11}, nil)
	d.projects.EXPECT().ProjectOrgID(gomock.Any(), snow.ID(42)).Return(snow.ID(1), nil)
	d.orgs.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(11)).Return(domain.OrgRoleMember, nil)

	data := strings.Repeat("x", 100)
	encoding, chunks, err := chunker.EncodeBytes([]byte(data), "a.txt", false, "")
	require.NoError(t, err)
	hashes := make([]domain.Hash, 0, len(chunks))
	for _, c := range chunks {
		hashes = append(hashes, c.Hash)
	}
	authored := time.Date(2019, 5, 6, 7, 8, 9, 0, time.UTC)
	var req usecase.ApplyPushRequest
	d.push.EXPECT().ApplyImport(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, r usecase.ApplyPushRequest) (*domain.Commit, error) {
		req = r
		return &domain.Commit{ID: r.CommitID, Hash: r.CommitHash, Origin: r.Origin}, nil
	})

	stream := &fakeImportStream{ctx: pushCtx(), msgs: []*pb.ImportStreamRequest{
		importHeader(&pb.ImportCommitHeader{
			Context: projectCtx(), Origin: origin('a'), ParentOrigins: []string{origin('b')}, Message: "child",
			AuthorName: "Ann", AuthorEmail: "ann@example.com", AuthoredAt: timestamppb.New(authored), AuthorUserId: strPtr(snow.ID(11).Base36()),
		}),
		importFiles(&pb.PushStreamFile{Path: "a.txt", Mode: pb.FileMode_FILE_MODE_EXECUTABLE, SizeBytes: int64(len(data)), Encoding: encoding, FileHash: raw(treehash.FileHash(hashes))}),
		importChunks(raw(hashes[0])),
		importRemoved("old.txt"),
		importEnd(),
		importHeader(&pb.ImportCommitHeader{Origin: origin('a')}),
		importEnd(),
	}}
	require.NoError(t, srv.ImportStream(stream))
	require.Len(t, stream.sent, 2)
	require.Equal(t, req.CommitID.Base36(), stream.sent[0].GetCommitId())
	require.Equal(t, origin('a'), stream.sent[0].GetOrigin())
	require.Equal(t, req.CommitHash.String(), stream.sent[0].GetCommitHash())
	require.Equal(t, snow.ID(1).Base36(), stream.sent[1].GetCommitId(), "a known origin is answered from the store")
	require.Equal(t, snow.ID(11), req.UserID)
	require.Equal(t, authored, *req.CreatedAt)
	require.Equal(t, "Ann", req.AuthorName)
	require.Len(t, req.Files, 1)
	require.Equal(t, 0o755, req.Files[0].Mode)
}

func TestImportStream_Errors(t *testing.T) {
	header := importHeader(&pb.ImportCommitHeader{Context: projectCtx(), Origin: origin('a')})
	cases := []struct {
		name  string
		admin bool
		msgs  []*pb.ImportStreamRequest
		code  codes.Code
	}{
		{name: "no context", admin: true, msgs: []*pb.ImportStreamRequest{importHeader(&pb.ImportCommitHeader{Origin: origin('a')})}, code: codes.InvalidArgument},
		{name: "unknown project", admin: true, msgs: []*pb.ImportStreamRequest{importHeader(&pb.ImportCommitHeader{Context: &pb.ProjectContext{Org: "x", Project: "x"}})}, code: codes.Internal},
		{name: "not admin", msgs: []*pb.ImportStreamRequest{header}, code: codes.PermissionDenied},
		{name: "part before header", admin: true, msgs: []*pb.ImportStreamRequest{importRemoved("a")}, code: codes.InvalidArgument},
		{name: "end before header", admin: true, msgs: []*pb.ImportStreamRequest{importEnd()}, code: codes.InvalidArgument},
		{name: "two headers", admin: true, msgs: []*pb.ImportStreamRequest{header, header}, code: codes.InvalidArgument},
		{name: "ends inside a commit", admin: true, msgs: []*pb.ImportStreamRequest{header}, code: codes.InvalidArgument},
		{name: "empty part", admin: true, msgs: []*pb.ImportStreamRequest{header, {}}, code: codes.InvalidArgument},
		{name: "chunks before files", admin: true, msgs: []*pb.ImportStreamRequest{header, importChunks(raw(domain.Hash{1}))}, code: codes.InvalidArgument},
		{name: "bad file hash", admin: true, msgs: []*pb.ImportStreamRequest{header, importFiles(&pb.PushStreamFile{Path: "a", FileHash: []byte{1}})}, code: codes.InvalidArgument},
		{name: "bad author id", admin: true, msgs: []*pb.ImportStreamRequest{
			importHeader(&pb.ImportCommitHeader{Context: projectCtx(), Origin: origin('a'), AuthorUserId: strPtr("!!")}), importEnd(),
		}, code: codes.InvalidArgument},
		{name: "invalid origin", admin: true, msgs: []*pb.ImportStreamRequest{
			importHeader(&pb.ImportCommitHeader{Context: projectCtx(), Origin: "git:x:y"}), importEnd(),
		}, code: codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, d := newImportServer(t)
			d.admin(tc.admin)
			err := srv.ImportStream(&fakeImportStream{ctx: pushCtx(), msgs: tc.msgs})
			require.Equal(t, tc.code, status.Code(err), err)
		})
	}

	t.Run("too many files", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(true)
		srv.uc.Import().WithMaxFiles(1)
		err := srv.ImportStream(&fakeImportStream{ctx: pushCtx(), msgs: []*pb.ImportStreamRequest{header, importRemoved("a", "b")}})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
	t.Run("empty stream", func(t *testing.T) {
		srv, _ := newImportServer(t)
		require.NoError(t, srv.ImportStream(&fakeImportStream{ctx: pushCtx()}))
	})
	t.Run("receive error", func(t *testing.T) {
		srv, _ := newImportServer(t)
		cancelled := status.Error(codes.Canceled, "gone")
		require.Equal(t, cancelled, srv.ImportStream(&fakeImportStream{ctx: pushCtx(), recvErr: cancelled}))
	})
	t.Run("send error", func(t *testing.T) {
		srv, d := newImportServer(t)
		d.admin(true)
		d.branches.EXPECT().CommitsByOrigins(gomock.Any(), snow.ID(42), gomock.Any()).Return([]*domain.Commit{{ID: 1, ProjectID: 42, Origin: origin('a')}}, nil)
		err := srv.ImportStream(&fakeImportStream{ctx: pushCtx(), sendErr: errors.New("closed"), msgs: []*pb.ImportStreamRequest{header, importEnd()}})
		require.EqualError(t, err, "closed")
	})
}
