package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

type importServer struct {
	pb.UnimplementedNipaServiceServer
	mu         sync.Mutex
	lookups    []*pb.ImportLookupRequest
	lookupErr  error
	lookupResp func(req *pb.ImportLookupRequest) *pb.ImportLookupResponse
	refsReq    *pb.ImportSetRefsRequest
	refsErr    error
	streams    int
	auth       []string
	headers    []*pb.ImportCommitHeader
	parts      int
	handle     func(n int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error
}

func (f *importServer) ImportLookup(_ context.Context, req *pb.ImportLookupRequest) (*pb.ImportLookupResponse, error) {
	f.lookups = append(f.lookups, req)
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.lookupResp(req), nil
}

func (f *importServer) ImportSetRefs(_ context.Context, req *pb.ImportSetRefsRequest) (*pb.ImportSetRefsResponse, error) {
	f.refsReq = req
	if f.refsErr != nil {
		return nil, f.refsErr
	}
	return &pb.ImportSetRefsResponse{Refs: []*pb.ImportRefResult{{Kind: "branch", Name: "main", Status: "created", CommitId: "1"}}}, nil
}

func (f *importServer) ImportStream(stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
	f.mu.Lock()
	f.streams++
	n := f.streams
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.auth = append(f.auth, md.Get("authorization")...)
	f.mu.Unlock()
	if f.handle != nil {
		return f.handle(n, stream)
	}
	return f.answerAll(stream)
}

// answerAll acknowledges every commit at its end marker.
func (f *importServer) answerAll(stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
	var origin string
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		f.mu.Lock()
		switch part := msg.GetPart().(type) {
		case *pb.ImportStreamRequest_Header:
			f.headers = append(f.headers, part.Header)
			origin = part.Header.GetOrigin()
		case *pb.ImportStreamRequest_End:
			id := snow.ID(len(f.headers))
			f.mu.Unlock()
			if err := stream.Send(&pb.ImportStreamResponse{Origin: origin, CommitId: id.Base36(), CommitHash: serverDomain.Hash{byte(id)}.String()}); err != nil {
				return err
			}
			continue
		default:
			f.parts++
		}
		f.mu.Unlock()
	}
}

func importOrigin(n int) string { return fmt.Sprintf("git:default:%040x", n) }

func TestClient_ImportLookup(t *testing.T) {
	fs := &importServer{lookupResp: func(req *pb.ImportLookupRequest) *pb.ImportLookupResponse {
		return &pb.ImportLookupResponse{Commits: []*pb.ImportedCommit{{Origin: req.GetOrigins()[0], CommitId: snow.ID(9).Base36(), CommitHash: serverDomain.Hash{9}.String()}}}
	}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	origins := make([]string, 1500)
	for i := range origins {
		origins[i] = importOrigin(i)
	}
	found, err := c.ImportLookup(context.Background(), "org", "proj", origins)
	require.NoError(t, err)
	require.Len(t, fs.lookups, 2, "lookups are batched by 1000")
	require.Len(t, fs.lookups[0].GetOrigins(), 1000)
	require.Equal(t, "proj", fs.lookups[1].GetContext().GetProject())
	require.Equal(t, ImportedCommit{Origin: importOrigin(1000), CommitID: 9, CommitHash: serverDomain.Hash{9}}, found[importOrigin(1000)])
	require.Len(t, found, 2)

	fs.lookupResp = func(*pb.ImportLookupRequest) *pb.ImportLookupResponse {
		return &pb.ImportLookupResponse{Commits: []*pb.ImportedCommit{{Origin: "x", CommitId: "!", CommitHash: ""}}}
	}
	_, err = c.ImportLookup(context.Background(), "org", "proj", origins[:1])
	require.ErrorContains(t, err, "invalid commit id")
	fs.lookupResp = func(*pb.ImportLookupRequest) *pb.ImportLookupResponse {
		return &pb.ImportLookupResponse{Commits: []*pb.ImportedCommit{{Origin: "x", CommitId: "1", CommitHash: "zz"}}}
	}
	_, err = c.ImportLookup(context.Background(), "org", "proj", origins[:1])
	require.ErrorContains(t, err, "invalid commit hash")

	fs.lookupResp = func(*pb.ImportLookupRequest) *pb.ImportLookupResponse { return &pb.ImportLookupResponse{} }
	fs.lookups = nil
	found, err = c.ImportLookup(context.Background(), "org", "proj", nil)
	require.NoError(t, err)
	require.Empty(t, found)
	require.Len(t, fs.lookups, 1, "an empty lookup still asks the server, which checks access")

	fs.lookupErr = status.Error(codes.PermissionDenied, "no")
	_, err = c.ImportLookup(context.Background(), "org", "proj", origins[:1])
	require.ErrorContains(t, err, "no")
}

func TestClient_ImportEstimate(t *testing.T) {
	fs := &importServer{lookupResp: func(req *pb.ImportLookupRequest) *pb.ImportLookupResponse {
		return &pb.ImportLookupResponse{QuotaChecked: req.GetEstimatedUploadBytes() > 0}
	}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	checked, err := c.ImportEstimate(context.Background(), "org", "proj", 2048)
	require.NoError(t, err)
	require.True(t, checked)
	require.Len(t, fs.lookups, 1)
	require.Equal(t, int64(2048), fs.lookups[0].GetEstimatedUploadBytes())
	require.Empty(t, fs.lookups[0].GetOrigins())
	require.Equal(t, "org", fs.lookups[0].GetContext().GetOrg())

	fs.lookupErr = status.Error(codes.ResourceExhausted, "storage quota exceeded")
	_, err = c.ImportEstimate(context.Background(), "org", "proj", 2048)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Equal(t, 402, de.Code)

	_, err = NewClient(NewTransport(), &stubSession{}).ImportEstimate(context.Background(), "org", "proj", 1)
	require.Error(t, err)
}

func TestClient_ImportOlderServer(t *testing.T) {
	c := connectedClient(t, &fakeServer{}, &stubSession{accessToken: "tok"})
	_, err := c.ImportLookup(context.Background(), "org", "proj", []string{importOrigin(1)})
	require.ErrorContains(t, err, "does not support imports")
	_, err = c.ImportSetRefs(context.Background(), "org", "proj", ImportRefs{})
	require.ErrorContains(t, err, "does not support imports")

	s, err := c.OpenImport(context.Background(), "org", "proj")
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
	_, err = s.Recv()
	require.ErrorContains(t, err, "does not support imports")
}

func TestClient_ImportSetRefs(t *testing.T) {
	fs := &importServer{}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	results, err := c.ImportSetRefs(context.Background(), "org", "proj", ImportRefs{
		Branches:      []ImportRef{{Name: "main", Origin: importOrigin(1)}},
		Tags:          []ImportRef{{Name: "v1", Origin: importOrigin(2), Message: "notes"}},
		DefaultBranch: "main",
		Update:        true,
		CommitCount:   4,
	})
	require.NoError(t, err)
	require.Equal(t, []ImportRefResult{{Kind: "branch", Name: "main", Status: "created", CommitID: "1"}}, results)
	require.Equal(t, "notes", fs.refsReq.GetTags()[0].GetMessage())
	require.Equal(t, "main", fs.refsReq.GetDefaultBranch())
	require.True(t, fs.refsReq.GetUpdate())
	require.EqualValues(t, 4, fs.refsReq.GetCommitCount())

	fs.refsErr = status.Error(codes.InvalidArgument, "commit not imported")
	_, err = c.ImportSetRefs(context.Background(), "org", "proj", ImportRefs{})
	require.ErrorContains(t, err, "commit not imported")
}

func TestImportSession_StreamsCommits(t *testing.T) {
	fs := &importServer{}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	s, err := c.OpenImport(context.Background(), "org", "proj")
	require.NoError(t, err)
	defer s.Close()

	authored := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	many := make([]serverDomain.Hash, maxHashesPerPart+5)
	require.NoError(t, s.Send(&ImportCommit{
		Origin: importOrigin(1), Message: "root", AuthorName: "Ann", AuthorEmail: "ann@x", AuthoredAt: authored, AuthorUserID: "b",
		Files:   []*serverDomain.PushFile{{Path: "big.bin", SizeBytes: 1, ChunkHashes: many}},
		Removed: []string{"gone"},
	}))
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(2), ParentOrigins: []string{importOrigin(1)}}))
	require.NoError(t, s.CloseSend())
	require.ErrorContains(t, s.Send(&ImportCommit{Origin: importOrigin(3)}), "closed")

	first, err := s.Recv()
	require.NoError(t, err)
	require.Equal(t, &ImportedCommit{Origin: importOrigin(1), CommitID: 1, CommitHash: serverDomain.Hash{1}}, first)
	second, err := s.Recv()
	require.NoError(t, err)
	require.Equal(t, importOrigin(2), second.Origin)
	_, err = s.Recv()
	require.ErrorIs(t, err, io.EOF)

	require.Equal(t, []string{"Bearer tok"}, fs.auth)
	require.Len(t, fs.headers, 2)
	h := fs.headers[0]
	require.Equal(t, "proj", h.GetContext().GetProject(), "the first header names the project")
	require.Nil(t, fs.headers[1].GetContext())
	require.Equal(t, authored, h.GetAuthoredAt().AsTime())
	require.Equal(t, "b", h.GetAuthorUserId())
	require.Equal(t, "Ann", h.GetAuthorName())
	require.Equal(t, []string{importOrigin(1)}, fs.headers[1].GetParentOrigins())
	require.Equal(t, 3, fs.parts, "files, a chunk continuation and removed paths")
}

func refuseFirst(err error, fs *importServer) func(int, grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
	return func(n int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
		if n == 1 {
			return err
		}
		return fs.answerAll(stream)
	}
}

func TestImportSession_RefreshesARefusedToken(t *testing.T) {
	fs := &importServer{}
	fs.handle = refuseFirst(status.Error(codes.Unauthenticated, "token expired"), fs)
	session := &stubSession{accessToken: "old", refreshToken: "new"}
	c := connectedClient(t, fs, session)
	s, err := c.OpenImport(context.Background(), "org", "proj")
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(2)}))
	require.NoError(t, s.CloseSend())
	for i := 1; i <= 2; i++ {
		got, err := s.Recv()
		require.NoError(t, err)
		require.Equal(t, importOrigin(i), got.Origin)
	}
	_, err = s.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, 1, session.refreshCalls)
	require.Equal(t, []string{"Bearer old", "Bearer new"}, fs.auth)
	require.Equal(t, "proj", fs.headers[0].GetContext().GetProject(), "a reopened stream names the project again")
}

func TestImportSession_RefreshFailure(t *testing.T) {
	fs := &importServer{}
	fs.handle = refuseFirst(status.Error(codes.Unauthenticated, "token expired"), fs)
	c := connectedClient(t, fs, &stubSession{accessToken: "old", refreshErr: errors.New("refresh failed")})
	s, err := c.OpenImport(context.Background(), "org", "proj")
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
	_, err = s.Recv()
	require.EqualError(t, err, "refresh failed")
}

func TestImportSession_WaitsOutTheRateLimit(t *testing.T) {
	fs := &importServer{}
	fs.handle = refuseFirst(status.Error(codes.ResourceExhausted, serverDomain.RateLimitMessage+", retry in 1s"), fs)
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	c.retry = fastRetry(3)
	s, err := c.OpenImport(context.Background(), "org", "proj")
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
	got, err := s.Recv()
	require.NoError(t, err)
	require.Equal(t, importOrigin(1), got.Origin)
	require.Equal(t, 2, fs.streams)
}

func TestImportSession_ErrorsAfterAnAnswerAreFinal(t *testing.T) {
	fs := &importServer{}
	fs.handle = func(_ int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
		for {
			msg, err := stream.Recv()
			if err != nil {
				return err
			}
			if msg.GetEnd() != nil {
				if err := stream.Send(&pb.ImportStreamResponse{Origin: importOrigin(1), CommitId: "1", CommitHash: serverDomain.Hash{}.String()}); err != nil {
					return err
				}
				return status.Error(codes.Unauthenticated, "late")
			}
		}
	}
	session := &stubSession{accessToken: "tok"}
	c := connectedClient(t, fs, session)
	s, err := c.OpenImport(context.Background(), "org", "proj")
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
	require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(2)}))
	_, err = s.Recv()
	require.NoError(t, err)
	_, err = s.Recv()
	require.Error(t, err)
	require.Zero(t, session.refreshCalls)
}

func TestImportSession_ProtocolErrors(t *testing.T) {
	t.Run("unexpected origin", func(t *testing.T) {
		fs := &importServer{}
		fs.handle = func(_ int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			return stream.Send(&pb.ImportStreamResponse{Origin: "other"})
		}
		c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
		s, err := c.OpenImport(context.Background(), "org", "proj")
		require.NoError(t, err)
		defer s.Close()
		require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
		_, err = s.Recv()
		require.ErrorContains(t, err, "unexpected commit")
	})
	t.Run("invalid answer", func(t *testing.T) {
		fs := &importServer{}
		fs.handle = func(_ int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			return stream.Send(&pb.ImportStreamResponse{Origin: importOrigin(1), CommitId: "!"})
		}
		c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
		s, err := c.OpenImport(context.Background(), "org", "proj")
		require.NoError(t, err)
		defer s.Close()
		require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
		_, err = s.Recv()
		require.ErrorContains(t, err, "invalid commit id")
	})
	t.Run("closed with unanswered commits", func(t *testing.T) {
		fs := &importServer{}
		fs.handle = func(_ int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
			_, err := stream.Recv()
			return err
		}
		c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
		s, err := c.OpenImport(context.Background(), "org", "proj")
		require.NoError(t, err)
		defer s.Close()
		require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
		_, err = s.Recv()
		require.ErrorContains(t, err, "unanswered")
	})
	t.Run("usecase error", func(t *testing.T) {
		fs := &importServer{}
		fs.handle = func(_ int, stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			return status.Error(codes.InvalidArgument, "parent not imported")
		}
		c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
		s, err := c.OpenImport(context.Background(), "org", "proj")
		require.NoError(t, err)
		defer s.Close()
		require.NoError(t, s.Send(&ImportCommit{Origin: importOrigin(1)}))
		_, err = s.Recv()
		require.ErrorContains(t, err, "parent not imported")
		require.Equal(t, 1, fs.streams)
	})
}

func TestOpenImport_Errors(t *testing.T) {
	c := connectedClient(t, &importServer{}, &stubSession{getTokenErr: errors.New("no token")})
	_, err := c.OpenImport(context.Background(), "org", "proj")
	require.Error(t, err)

	_, err = NewClient(NewTransport(), &stubSession{}).OpenImport(context.Background(), "org", "proj")
	require.Error(t, err)
	_, err = NewClient(NewTransport(), &stubSession{}).ImportLookup(context.Background(), "org", "proj", nil)
	require.Error(t, err)
	_, err = NewClient(NewTransport(), &stubSession{}).ImportSetRefs(context.Background(), "org", "proj", ImportRefs{})
	require.Error(t, err)
	require.True(t, strings.Contains(importFeature, "import"))
}
