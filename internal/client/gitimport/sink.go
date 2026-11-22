package gitimport

import (
	"context"
	"sync"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const (
	lookupBatch   = 1000
	pipelineDepth = 2
)

type Transport interface {
	ImportLookup(ctx context.Context, org, project string, origins []string) (map[string]grpc.ImportedCommit, error)
	ImportEstimate(ctx context.Context, org, project string, bytes int64) (bool, error)
	OpenImport(ctx context.Context, org, project string) (CommitStream, error)
	ImportSetRefs(ctx context.Context, org, project string, refs grpc.ImportRefs) ([]grpc.ImportRefResult, error)
	UploadChunks(ctx context.Context, scope domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error)
}

type CommitStream interface {
	Send(commit *grpc.ImportCommit) error
	Recv() (*grpc.ImportedCommit, error)
	CloseSend() error
	Close()
}

type grpcTransport struct {
	c *grpc.Client
}

func NewGRPCTransport(c *grpc.Client) Transport {
	return grpcTransport{c: c}
}

func (t grpcTransport) ImportLookup(ctx context.Context, org, project string, origins []string) (map[string]grpc.ImportedCommit, error) {
	return t.c.ImportLookup(ctx, org, project, origins)
}

func (t grpcTransport) ImportEstimate(ctx context.Context, org, project string, bytes int64) (bool, error) {
	return t.c.ImportEstimate(ctx, org, project, bytes)
}

func (t grpcTransport) OpenImport(ctx context.Context, org, project string) (CommitStream, error) {
	return t.c.OpenImport(ctx, org, project)
}

func (t grpcTransport) ImportSetRefs(ctx context.Context, org, project string, refs grpc.ImportRefs) ([]grpc.ImportRefResult, error) {
	return t.c.ImportSetRefs(ctx, org, project, refs)
}

// UploadWindowBytes forwards the client's upload window override to the
// chunk uploader.
func (t grpcTransport) UploadWindowBytes() int {
	return t.c.UploadWindowBytes()
}

func (t grpcTransport) UploadChunks(ctx context.Context, scope domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	return t.c.UploadChunks(ctx, scope, chunks, onChunk...)
}

type Sink struct {
	transport Transport
	org       string
	project   string
}

func NewSink(t Transport, org, project string) *Sink {
	return &Sink{transport: t, org: org, project: project}
}

func (s *Sink) Lookup(ctx context.Context, origins []string) (map[string]bool, error) {
	known := make(map[string]bool)
	for start := 0; start == 0 || start < len(origins); start += lookupBatch {
		found, err := s.transport.ImportLookup(ctx, s.org, s.project, origins[start:min(start+lookupBatch, len(origins))])
		if err != nil {
			return nil, err
		}
		for origin := range found {
			known[origin] = true
		}
	}
	return known, nil
}

func (s *Sink) Estimate(ctx context.Context, bytes int64) (bool, error) {
	return s.transport.ImportEstimate(ctx, s.org, s.project, bytes)
}

func (s *Sink) Uploader(ctx context.Context, onChunk func(*serverDomain.ChunkData)) *usecase.ChunkUploader {
	return usecase.NewChunkUploader(ctx, s.transport, domain.ChunkScope{Org: s.org, Project: s.project}, onChunk)
}

type CommitSource func(ctx context.Context, i int) (*grpc.ImportCommit, error)

func (s *Sink) Commit(parent context.Context, n int, next CommitSource, done func(i int, c *grpc.ImportedCommit)) error {
	if n == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, err := s.transport.OpenImport(ctx, s.org, s.project)
	if err != nil {
		return err
	}
	defer stream.Close()

	var once sync.Once
	var first error
	fail := func(err error) {
		once.Do(func() {
			first = err
			cancel()
		})
	}
	slots := make(chan struct{}, pipelineDepth)
	received := make(chan struct{})
	go func() {
		defer close(received)
		for i := 0; i < n; i++ {
			res, err := stream.Recv()
			if err != nil {
				fail(err)
				return
			}
			<-slots
			if done != nil {
				done(i, res)
			}
		}
	}()

	if err := s.send(ctx, stream, n, next, slots); err != nil {
		fail(err)
		stream.Close()
	}
	<-received
	if err := parent.Err(); err != nil {
		return err
	}
	return first
}

func (s *Sink) send(ctx context.Context, stream CommitStream, n int, next CommitSource, slots chan struct{}) error {
	for i := 0; i < n; i++ {
		commit, err := next(ctx, i)
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := stream.Send(commit); err != nil {
			return err
		}
	}
	return stream.CloseSend()
}

func (s *Sink) SetRefs(ctx context.Context, refs grpc.ImportRefs) ([]grpc.ImportRefResult, error) {
	return s.transport.ImportSetRefs(ctx, s.org, s.project, refs)
}
