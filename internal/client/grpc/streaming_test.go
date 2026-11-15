package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func (f *fakeServer) PushStream(stream grpc.ClientStreamingServer[pb.PushStreamRequest, pb.PushResponse]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.pushAuth = append(f.pushAuth, md.Get("authorization")...)
	if len(f.pushErrs) > 0 {
		err := f.pushErrs[0]
		f.pushErrs = f.pushErrs[1:]
		return err
	}
	f.pushParts = nil
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		f.pushParts = append(f.pushParts, msg)
		if f.pushFailAfterHeader != nil {
			return f.pushFailAfterHeader
		}
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	return stream.SendAndClose(f.pushResp)
}

type assembledPush struct {
	header  *pb.PushStreamHeader
	files   []*pb.PushStreamFile
	removed []string
}

func assemblePush(t *testing.T, parts []*pb.PushStreamRequest) assembledPush {
	t.Helper()
	require.NotEmpty(t, parts)
	out := assembledPush{header: parts[0].GetHeader()}
	require.NotNil(t, out.header, "the first part must be the header")
	for _, p := range parts[1:] {
		switch part := p.GetPart().(type) {
		case *pb.PushStreamRequest_Files:
			out.files = append(out.files, part.Files.GetFiles()...)
		case *pb.PushStreamRequest_Chunks:
			require.NotEmpty(t, out.files)
			last := out.files[len(out.files)-1]
			last.ChunkHashes = append(last.ChunkHashes, part.Chunks.GetChunkHashes()...)
		case *pb.PushStreamRequest_Removed:
			out.removed = append(out.removed, part.Removed.GetPaths()...)
		default:
			t.Fatalf("unexpected part %T", part)
		}
	}
	return out
}

func okPushResp() *pb.PushResponse {
	return &pb.PushResponse{
		CommitId:   snow.ID(1).Base36(),
		CommitHash: serverDomain.Hash{}.String(),
		TreeHash:   serverDomain.Hash{}.String(),
	}
}

func connectedClient(t *testing.T, fs pb.NipaServiceServer, session *stubSession) *Client {
	t.Helper()
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), session)
	require.NoError(t, c.Connect(context.Background(), addr))
	return c
}

func TestClient_Push_Success(t *testing.T) {
	var chunkHash serverDomain.Hash
	chunkHash[0] = 0xab
	var fileHash serverDomain.Hash
	fileHash[31] = 0xcd

	fs := &fakeServer{pushResp: &pb.PushResponse{
		CommitId:   snow.ID(7).Base36(),
		CommitHash: fileHash.String(),
		TreeHash:   chunkHash.String(),
	}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})

	got, err := c.Push(context.Background(), "org", "project", "main", "base", "message",
		[]*serverDomain.PushFile{{
			Path:        "a.txt",
			Mode:        0o644,
			SizeBytes:   5,
			Encoding:    "zstd",
			FileHash:    fileHash,
			ChunkHashes: []serverDomain.Hash{chunkHash},
		}},
		[]string{"old.txt"},
		"parent2",
		"base-commit",
	)
	require.NoError(t, err)
	require.Equal(t, snow.ID(7), got.CommitID)
	require.Equal(t, fileHash, got.CommitHash)
	require.Equal(t, chunkHash, got.TreeHash)
	require.Equal(t, []string{"Bearer tok"}, fs.pushAuth)

	push := assemblePush(t, fs.pushParts)
	require.Equal(t, "org", push.header.GetContext().GetOrg())
	require.Equal(t, "project", push.header.GetContext().GetProject())
	require.Equal(t, "main", push.header.GetBranch())
	require.Equal(t, "base", push.header.GetBaseTreeHash())
	require.Equal(t, "message", push.header.GetMessage())
	require.Equal(t, "parent2", push.header.GetParent_2CommitHash())
	require.Equal(t, "base-commit", push.header.GetBaseCommitId())
	require.Equal(t, []string{"old.txt"}, push.removed)

	require.Len(t, push.files, 1)
	gotFile := push.files[0]
	require.Equal(t, "a.txt", gotFile.GetPath())
	require.Equal(t, pb.FileMode_FILE_MODE_READ_WRITE, gotFile.GetMode())
	require.Equal(t, int64(5), gotFile.GetSizeBytes())
	require.Equal(t, "zstd", gotFile.GetEncoding())
	require.Equal(t, fileHash[:], gotFile.GetFileHash())
	require.Equal(t, [][]byte{chunkHash[:]}, gotFile.GetChunkHashes())
}

func TestClient_Push_ExecutableMode(t *testing.T) {
	fs := &fakeServer{pushResp: okPushResp()}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m",
		[]*serverDomain.PushFile{{Path: "run.sh", Mode: 0o755, FileHash: serverDomain.Hash{}, ChunkHashes: []serverDomain.Hash{{0x01}}}}, nil, "", "")
	require.NoError(t, err)
	push := assemblePush(t, fs.pushParts)
	require.Equal(t, pb.FileMode_FILE_MODE_EXECUTABLE, push.files[0].GetMode())
	require.Nil(t, push.header.Parent_2CommitHash)
	require.Nil(t, push.header.BaseCommitId)
}

func TestClient_Push_LargeManifestIsSplit(t *testing.T) {
	const fileCount = 30000
	files := make([]*serverDomain.PushFile, 0, fileCount+1)
	for i := 0; i < fileCount; i++ {
		var h serverDomain.Hash
		h[0], h[1] = byte(i), byte(i>>8)
		files = append(files, &serverDomain.PushFile{
			Path:        fmt.Sprintf("node_modules/pkg-%05d/lib/index.js", i),
			Mode:        0o644,
			SizeBytes:   10,
			FileHash:    h,
			ChunkHashes: []serverDomain.Hash{h, h},
		})
	}
	huge := &serverDomain.PushFile{Path: "video.mov", Mode: 0o644, SizeBytes: 1 << 40, IsBinary: true}
	for i := 0; i < 2*maxHashesPerPart+5; i++ {
		var h serverDomain.Hash
		h[0], h[1], h[2] = byte(i), byte(i>>8), byte(i>>16)
		huge.ChunkHashes = append(huge.ChunkHashes, h)
	}
	files = append(files, huge)
	removed := make([]string, 50000)
	for i := range removed {
		removed[i] = fmt.Sprintf("old/dir/file-%06d.txt", i)
	}

	fs := &fakeServer{pushResp: okPushResp()}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", files, removed, "", "")
	require.NoError(t, err)

	require.Greater(t, len(fs.pushParts), 5)
	for _, part := range fs.pushParts {
		require.LessOrEqual(t, proto.Size(part), pushPartBudget+64)
	}
	push := assemblePush(t, fs.pushParts)
	require.Len(t, push.files, fileCount+1)
	require.Equal(t, removed, push.removed)
	for i, f := range files {
		require.Equal(t, f.Path, push.files[i].GetPath())
		require.Len(t, push.files[i].GetChunkHashes(), len(f.ChunkHashes), f.Path)
	}
	last := push.files[fileCount].GetChunkHashes()
	for i, h := range huge.ChunkHashes {
		require.Equal(t, h[:], last[i])
	}
}

func TestClient_Push_ServerError(t *testing.T) {
	c := connectedClient(t, &fakeServer{pushErr: status.Error(codes.FailedPrecondition, "branch moved")}, &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "stale", "m", nil, nil, "", "")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "branch moved", domErr.Message)
}

func TestClient_Push_ServerRejectsHeaderWhileClientStillSends(t *testing.T) {
	files := make([]*serverDomain.PushFile, 40000)
	for i := range files {
		files[i] = &serverDomain.PushFile{Path: fmt.Sprintf("f-%06d", i), ChunkHashes: []serverDomain.Hash{{1}}}
	}
	fs := &fakeServer{pushFailAfterHeader: status.Error(codes.PermissionDenied, "no write access")}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", files, nil, "", "")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
	require.Equal(t, "no write access", domErr.Message)
}

func TestClient_Push_RefreshesExpiredToken(t *testing.T) {
	fs := &fakeServer{
		pushResp: okPushResp(),
		pushErrs: []error{status.Error(codes.Unauthenticated, "token expired")},
	}
	session := &stubSession{accessToken: "old", refreshToken: "new"}
	c := connectedClient(t, fs, session)

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m",
		[]*serverDomain.PushFile{{Path: "a.txt", ChunkHashes: []serverDomain.Hash{{1}}}}, []string{"b.txt"}, "", "")
	require.NoError(t, err)
	require.Equal(t, 1, session.refreshCalls)
	require.Equal(t, []string{"Bearer old", "Bearer new"}, fs.pushAuth)
	push := assemblePush(t, fs.pushParts)
	require.Len(t, push.files, 1)
	require.Equal(t, []string{"b.txt"}, push.removed)
}

func TestClient_Push_RefreshFails(t *testing.T) {
	fs := &fakeServer{pushErrs: []error{status.Error(codes.Unauthenticated, "token expired")}}
	session := &stubSession{accessToken: "old", refreshErr: errors.New("refresh failed")}
	c := connectedClient(t, fs, session)

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	require.EqualError(t, err, "refresh failed")
}

func TestClient_Push_NoAccessToken(t *testing.T) {
	fs := &fakeServer{pushResp: okPushResp()}
	c := connectedClient(t, fs, &stubSession{getTokenErr: errors.New("no token")})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 401, domErr.Code)
	require.Empty(t, fs.pushAuth)
}

type legacyPushServer struct {
	pb.UnimplementedNipaServiceServer
}

func TestClient_Push_ServerWithoutStreaming(t *testing.T) {
	c := connectedClient(t, &legacyPushServer{}, &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "the server does not support streamed pushes", domErr.Message)
	require.Contains(t, domErr.Hint, "upgrade the nipa server")
}

func TestClient_Push_InvalidResponseHash(t *testing.T) {
	fs := &fakeServer{pushResp: &pb.PushResponse{CommitId: snow.ID(1).Base36(), CommitHash: "zz"}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	require.Error(t, err)

	fs.pushResp = &pb.PushResponse{CommitId: snow.ID(1).Base36(), CommitHash: serverDomain.Hash{}.String(), TreeHash: "zz"}
	_, err = c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	require.Error(t, err)
}

func TestClient_Push_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestSendPushParts_StopsOnSendError(t *testing.T) {
	boom := errors.New("boom")
	files := []*serverDomain.PushFile{{Path: "a"}, {Path: "b", ChunkHashes: make([]serverDomain.Hash, maxHashesPerPart+1)}}
	for failAt := 0; failAt < 4; failAt++ {
		sent := 0
		send := func(*pb.PushStreamRequest) error {
			if sent == failAt {
				return boom
			}
			sent++
			return nil
		}
		require.ErrorIs(t, sendPushParts(send, &pb.PushStreamHeader{}, files, []string{"gone"}), boom, "fail at %d", failAt)
	}

	sent := 0
	removed := make([]string, 200000)
	for i := range removed {
		removed[i] = fmt.Sprintf("dir/%07d", i)
	}
	send := func(*pb.PushStreamRequest) error {
		if sent == 2 {
			return boom
		}
		sent++
		return nil
	}
	require.ErrorIs(t, sendPushParts(send, &pb.PushStreamHeader{}, nil, removed), boom)
}

func TestAuthedContext_NoToken(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{getTokenErr: status.Error(codes.NotFound, "no token")})

	_, err := c.authedContext(context.Background())
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
