package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	importFeature     = "imports"
	importLookupBatch = 1000
)

// ImportCommit is one source commit streamed to the server: its files and
// removed paths are the delta against the first parent's tree.
type ImportCommit struct {
	Origin        string
	ParentOrigins []string
	Message       string
	AuthorName    string
	AuthorEmail   string
	AuthoredAt    time.Time
	// AuthorUserID is the base36 id of the mapped Nipa user, if any.
	AuthorUserID string
	Files        []*serverDomain.PushFile
	Removed      []string
}

type ImportedCommit struct {
	Origin     string
	CommitID   snow.ID
	CommitHash serverDomain.Hash
}

type ImportRef struct {
	Name    string
	Origin  string
	Message string
}

type ImportRefs struct {
	Branches      []ImportRef
	Tags          []ImportRef
	DefaultBranch string
	Update        bool
	CommitCount   int
}

type ImportRefResult struct {
	Kind     string
	Name     string
	Status   string
	CommitID string
	Message  string
}

// ImportLookup returns the commits the server already holds for origins.
func (c *Client) ImportLookup(ctx context.Context, org, project string, origins []string) (map[string]ImportedCommit, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	found := make(map[string]ImportedCommit, len(origins))
	for start := 0; start == 0 || start < len(origins); start += importLookupBatch {
		end := min(start+importLookupBatch, len(origins))
		res, err := client.ImportLookup(ctx, &pb.ImportLookupRequest{
			Context: &pb.ProjectContext{Org: org, Project: project},
			Origins: origins[start:end],
		})
		if err != nil {
			return nil, streamError(err, importFeature)
		}
		for _, ic := range res.GetCommits() {
			commit, err := toImportedCommit(ic.GetOrigin(), ic.GetCommitId(), ic.GetCommitHash())
			if err != nil {
				return nil, err
			}
			found[commit.Origin] = commit
		}
	}
	return found, nil
}

// ImportEstimate asks the server whether the storage quota has room for an
// import that uploads at most bytes new bytes. It reports whether a quota was
// checked: servers without a quota, or older ones, answer false.
func (c *Client) ImportEstimate(ctx context.Context, org, project string, bytes int64) (bool, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return false, err
	}
	res, err := client.ImportLookup(ctx, &pb.ImportLookupRequest{
		Context:              &pb.ProjectContext{Org: org, Project: project},
		EstimatedUploadBytes: bytes,
	})
	if err != nil {
		return false, streamError(err, importFeature)
	}
	return res.GetQuotaChecked(), nil
}

// ImportSetRefs points branches and tags at imported commits.
func (c *Client) ImportSetRefs(ctx context.Context, org, project string, refs ImportRefs) ([]ImportRefResult, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ImportSetRefs(ctx, &pb.ImportSetRefsRequest{
		Context:       &pb.ProjectContext{Org: org, Project: project},
		Branches:      toPBImportRefs(refs.Branches),
		Tags:          toPBImportRefs(refs.Tags),
		DefaultBranch: refs.DefaultBranch,
		Update:        refs.Update,
		CommitCount:   int64(refs.CommitCount),
	})
	if err != nil {
		return nil, streamError(err, importFeature)
	}
	out := make([]ImportRefResult, 0, len(res.GetRefs()))
	for _, r := range res.GetRefs() {
		out = append(out, ImportRefResult{Kind: r.GetKind(), Name: r.GetName(), Status: r.GetStatus(), CommitID: r.GetCommitId(), Message: r.GetMessage()})
	}
	return out, nil
}

func toPBImportRefs(refs []ImportRef) []*pb.ImportRef {
	out := make([]*pb.ImportRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, &pb.ImportRef{Name: r.Name, Origin: r.Origin, Message: r.Message})
	}
	return out
}

func toImportedCommit(origin, commitID, commitHash string) (ImportedCommit, error) {
	id, err := snow.ParseBase36(commitID)
	if err != nil {
		return ImportedCommit{}, fmt.Errorf("server returned an invalid commit id %q", commitID)
	}
	hash, err := decodeHash(commitHash)
	if err != nil {
		return ImportedCommit{}, fmt.Errorf("server returned an invalid commit hash %q", commitHash)
	}
	return ImportedCommit{Origin: origin, CommitID: id, CommitHash: hash}, nil
}

type importStream = grpc.BidiStreamingClient[pb.ImportStreamRequest, pb.ImportStreamResponse]

// ImportSession streams commits to the server one after another. Send and
// Recv may run on different goroutines, so the caller can upload the next
// commit's content while the server applies the previous one. Every commit
// sent is answered by one Recv, in order.
type ImportSession struct {
	c       *Client
	client  pb.NipaServiceClient
	ctx     context.Context
	cancel  context.CancelFunc
	project *pb.ProjectContext

	mu        sync.Mutex
	stream    importStream
	answered  bool
	named     bool
	closed    bool
	refreshed bool
	throttles int
	pending   []*ImportCommit
}

// OpenImport starts an import stream into a project. A stream the server
// refuses before answering (expired token, rate limit) is reopened and the
// unanswered commits are sent again; commits are idempotent by origin.
func (c *Client) OpenImport(ctx context.Context, org, project string) (*ImportSession, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &ImportSession{c: c, client: client, ctx: ctx, cancel: cancel, project: &pb.ProjectContext{Org: org, Project: project}}
	token, err := c.session.AccessToken(ctx, c.transport.URL())
	if err != nil {
		cancel()
		return nil, toDomainError(status.Error(codes.Unauthenticated, "unable to get access token"))
	}
	if err := s.open(token); err != nil {
		cancel()
		return nil, streamError(err, importFeature)
	}
	return s, nil
}

func (s *ImportSession) open(token string) error {
	stream, err := s.client.ImportStream(withBearerToken(s.ctx, token))
	if err != nil {
		return err
	}
	s.stream, s.answered, s.named = stream, false, false
	return nil
}

// Send streams one commit. A stream the server already ended reports its
// error on the next Recv.
func (s *ImportSession) Send(commit *ImportCommit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("import session is closed for sending")
	}
	s.pending = append(s.pending, commit)
	if err := s.sendCommit(commit); err != nil && !errors.Is(err, io.EOF) {
		return streamError(err, importFeature)
	}
	return nil
}

func (s *ImportSession) sendCommit(commit *ImportCommit) error {
	header := &pb.ImportCommitHeader{
		Origin:        commit.Origin,
		ParentOrigins: commit.ParentOrigins,
		Message:       commit.Message,
		AuthorName:    commit.AuthorName,
		AuthorEmail:   commit.AuthorEmail,
	}
	if !s.named {
		header.Context = s.project
		s.named = true
	}
	if !commit.AuthoredAt.IsZero() {
		header.AuthoredAt = timestamppb.New(commit.AuthoredAt)
	}
	if commit.AuthorUserID != "" {
		header.AuthorUserId = &commit.AuthorUserID
	}
	stream := s.stream
	if err := stream.Send(&pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Header{Header: header}}); err != nil {
		return err
	}
	send := func(m *pb.PushStreamRequest) error {
		switch part := m.GetPart().(type) {
		case *pb.PushStreamRequest_Files:
			return stream.Send(&pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Files{Files: part.Files}})
		case *pb.PushStreamRequest_Chunks:
			return stream.Send(&pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Chunks{Chunks: part.Chunks}})
		case *pb.PushStreamRequest_Removed:
			return stream.Send(&pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_Removed{Removed: part.Removed}})
		}
		return nil
	}
	if err := sendPushFiles(send, commit.Files); err != nil {
		return err
	}
	if err := sendPushRemoved(send, commit.Removed); err != nil {
		return err
	}
	return stream.Send(&pb.ImportStreamRequest{Part: &pb.ImportStreamRequest_End{End: &pb.ImportCommitEnd{}}})
}

// Recv returns the stored commit for the oldest unanswered Send, or io.EOF
// once the stream closed with every commit answered.
func (s *ImportSession) Recv() (*ImportedCommit, error) {
	for {
		s.mu.Lock()
		stream := s.stream
		s.mu.Unlock()

		resp, err := stream.Recv()

		s.mu.Lock()
		if stream != s.stream {
			s.mu.Unlock()
			continue
		}
		if err == nil {
			commit, err := s.answer(resp)
			s.mu.Unlock()
			return commit, err
		}
		if errors.Is(err, io.EOF) {
			n := len(s.pending)
			s.mu.Unlock()
			if n > 0 {
				return nil, fmt.Errorf("import stream ended with %d commits unanswered", n)
			}
			return nil, io.EOF
		}
		retried, retryErr := s.retry(err)
		s.mu.Unlock()
		if retryErr != nil {
			return nil, retryErr
		}
		if !retried {
			return nil, streamError(err, importFeature)
		}
	}
}

func (s *ImportSession) answer(resp *pb.ImportStreamResponse) (*ImportedCommit, error) {
	s.answered = true
	if len(s.pending) == 0 || s.pending[0].Origin != resp.GetOrigin() {
		return nil, fmt.Errorf("server answered an unexpected commit %q", resp.GetOrigin())
	}
	s.pending = s.pending[1:]
	commit, err := toImportedCommit(resp.GetOrigin(), resp.GetCommitId(), resp.GetCommitHash())
	if err != nil {
		return nil, err
	}
	return &commit, nil
}

// retry reopens a stream the server refused before answering any commit,
// once after a token refresh and a bounded number of times after rate
// limiting, and sends the unanswered commits again.
func (s *ImportSession) retry(cause error) (bool, error) {
	if s.answered {
		return false, nil
	}
	var token string
	var err error
	if wait, limited := rateLimitWait(cause); limited && s.throttles < s.c.retry.throttleAttempts {
		if err := sleepContext(s.ctx, s.c.retry.throttleDelay(s.throttles, wait)); err != nil {
			return false, err
		}
		s.throttles++
		token, err = s.c.session.AccessToken(s.ctx, s.c.transport.URL())
	} else if status.Code(cause) == codes.Unauthenticated && !s.refreshed {
		s.refreshed = true
		token, err = s.c.session.Refresh(s.ctx, s.c.transport.URL())
	} else {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := s.open(token); err != nil {
		return false, streamError(err, importFeature)
	}
	for _, commit := range s.pending {
		if err := s.sendCommit(commit); err != nil && !errors.Is(err, io.EOF) {
			return false, streamError(err, importFeature)
		}
	}
	if s.closed {
		if err := s.stream.CloseSend(); err != nil {
			return false, streamError(err, importFeature)
		}
	}
	return true, nil
}

// CloseSend tells the server no more commits follow; Recv then drains the
// remaining answers and returns io.EOF.
func (s *ImportSession) CloseSend() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.stream.CloseSend()
}

// Close abandons the stream.
func (s *ImportSession) Close() {
	s.cancel()
}
