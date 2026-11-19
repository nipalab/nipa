package gitimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestSink_LookupBatches(t *testing.T) {
	srv := newFakeServer()
	srv.commits["git:default:x5"] = &fakeCommit{id: 1}
	origins := make([]string, 2500)
	for i := range origins {
		origins[i] = fmt.Sprintf("git:default:x%d", i)
	}
	known, err := NewSink(srv, "o", "p").Lookup(context.Background(), origins)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"git:default:x5": true}, known)
	require.Len(t, srv.lookups, 3)
	require.Len(t, srv.lookups[0], 1000)
	require.Len(t, srv.lookups[2], 500)

	srv.lookups = nil
	known, err = NewSink(srv, "o", "p").Lookup(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, known)
	require.Len(t, srv.lookups, 1)
}

type gatedStream struct {
	mu          sync.Mutex
	sent        []*grpc.ImportCommit
	outstanding atomic.Int32
	maxOut      atomic.Int32
	release     chan struct{}
	sendErr     error
	recvErr     error
	closed      chan struct{}
	once        sync.Once
}

func (g *gatedStream) Send(c *grpc.ImportCommit) error {
	if g.sendErr != nil {
		return g.sendErr
	}
	g.mu.Lock()
	g.sent = append(g.sent, c)
	g.mu.Unlock()
	n := g.outstanding.Add(1)
	for {
		m := g.maxOut.Load()
		if n <= m || g.maxOut.CompareAndSwap(m, n) {
			break
		}
	}
	return nil
}

func (g *gatedStream) Recv() (*grpc.ImportedCommit, error) {
	select {
	case <-g.release:
	case <-g.closed:
		return nil, io.EOF
	}
	if g.recvErr != nil {
		return nil, g.recvErr
	}
	g.outstanding.Add(-1)
	return &grpc.ImportedCommit{}, nil
}

func (g *gatedStream) CloseSend() error { return nil }
func (g *gatedStream) Close()           { g.once.Do(func() { close(g.closed) }) }

type streamTransport struct {
	fakeServer
	stream CommitStream
}

func (s *streamTransport) OpenImport(context.Context, string, string) (CommitStream, error) {
	return s.stream, nil
}

func (s *streamTransport) UploadChunks(context.Context, domain.ChunkScope, []*serverDomain.ChunkData, ...func(*serverDomain.ChunkData)) (int, int, error) {
	return 0, 0, nil
}

func newGated() *gatedStream {
	return &gatedStream{release: make(chan struct{}), closed: make(chan struct{})}
}

func TestSink_CommitPipelinesTwoCommits(t *testing.T) {
	g := newGated()
	sink := NewSink(&streamTransport{stream: g}, "o", "p")
	var converted atomic.Int32
	next := func(_ context.Context, i int) (*grpc.ImportCommit, error) {
		converted.Add(1)
		return &grpc.ImportCommit{Origin: fmt.Sprint(i)}, nil
	}
	done := make(chan error, 1)
	var answered []int
	go func() {
		done <- sink.Commit(context.Background(), 5, next, func(i int, _ *grpc.ImportedCommit) { answered = append(answered, i) })
	}()
	require.Eventually(t, func() bool { return converted.Load() == 3 }, defaultWait, pollEvery)
	require.EqualValues(t, 2, g.outstanding.Load(), "the third commit waits while two are applied")
	for range 5 {
		g.release <- struct{}{}
	}
	require.NoError(t, <-done)
	require.EqualValues(t, 2, g.maxOut.Load())
	require.Equal(t, []int{0, 1, 2, 3, 4}, answered)
	require.Len(t, g.sent, 5)
}

func TestSink_CommitErrors(t *testing.T) {
	ctx := context.Background()
	ok := func(_ context.Context, i int) (*grpc.ImportCommit, error) { return &grpc.ImportCommit{}, nil }

	require.NoError(t, NewSink(newFakeServer(), "o", "p").Commit(ctx, 0, nil, nil))

	g := newGated()
	err := NewSink(&streamTransport{stream: g}, "o", "p").Commit(ctx, 3, func(_ context.Context, i int) (*grpc.ImportCommit, error) {
		if i == 1 {
			return nil, errors.New("convert failed")
		}
		return &grpc.ImportCommit{}, nil
	}, nil)
	require.EqualError(t, err, "convert failed")

	g = newGated()
	g.sendErr = errors.New("send failed")
	err = NewSink(&streamTransport{stream: g}, "o", "p").Commit(ctx, 2, ok, nil)
	require.EqualError(t, err, "send failed")

	g = newGated()
	g.recvErr = errors.New("apply failed")
	close(g.release)
	err = NewSink(&streamTransport{stream: g}, "o", "p").Commit(ctx, 4, ok, nil)
	require.EqualError(t, err, "apply failed")

	cctx, cancel := context.WithCancel(ctx)
	g = newGated()
	err = NewSink(&streamTransport{stream: g}, "o", "p").Commit(cctx, 4, func(_ context.Context, i int) (*grpc.ImportCommit, error) {
		if i == 2 {
			cancel()
		}
		return &grpc.ImportCommit{}, nil
	}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestGRPCTransportDelegates(t *testing.T) {
	tr := NewGRPCTransport(nil)
	require.NotNil(t, tr)
	require.Panics(t, func() { _, _ = tr.ImportLookup(context.Background(), "o", "p", nil) })
	require.Panics(t, func() { _, _ = tr.ImportEstimate(context.Background(), "o", "p", 1) })
	require.Panics(t, func() { _, _ = tr.OpenImport(context.Background(), "o", "p") })
	require.Panics(t, func() { _, _ = tr.ImportSetRefs(context.Background(), "o", "p", grpc.ImportRefs{}) })
	require.Panics(t, func() {
		_, _, _ = tr.UploadChunks(context.Background(), domain.ChunkScope{}, []*serverDomain.ChunkData{{}})
	})
}

const (
	defaultWait = 5 * time.Second
	pollEvery   = time.Millisecond
)
