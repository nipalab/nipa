package grpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// streamRecorder is a ChunkStream that keeps what each attempt read.
type streamRecorder struct {
	mu       sync.Mutex
	want     map[serverDomain.Hash]bool
	attempts map[serverDomain.Hash]int
	data     map[serverDomain.Hash][]byte
	writeErr error
}

func newStreamRecorder(hashes ...serverDomain.Hash) *streamRecorder {
	r := &streamRecorder{want: map[serverDomain.Hash]bool{}, attempts: map[serverDomain.Hash]int{}, data: map[serverDomain.Hash][]byte{}}
	for _, h := range hashes {
		r.want[h] = true
	}
	return r
}

func (r *streamRecorder) stream() *domain.ChunkStream {
	return &domain.ChunkStream{
		Wants: func(h serverDomain.Hash) bool { return r.want[h] },
		Write: func(h serverDomain.Hash, body io.Reader) error {
			data, err := io.ReadAll(body)
			r.mu.Lock()
			defer r.mu.Unlock()
			r.attempts[h]++
			if err != nil {
				return err
			}
			if r.writeErr != nil {
				return r.writeErr
			}
			r.data[h] = data
			return nil
		},
	}
}

func TestDownloadChunksStream_SplitsStreamedAndBuffered(t *testing.T) {
	streamed, buffered := testChunkHash(0x40), testChunkHash(0x41)
	srv := newChunkTestServer()
	srv.put(streamed.String(), []byte("goes to the stream"))
	srv.put(buffered.String(), []byte("goes to the callback"))
	c := newDownloadClient(t, srv, fastRetry(3))

	rec := newStreamRecorder(streamed)
	got := newDeliveries()
	err := c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{streamed, buffered}, rec.stream(), got.sink)
	require.NoError(t, err)
	require.Equal(t, []byte("goes to the stream"), rec.data[streamed])
	require.NotContains(t, rec.data, buffered)
	require.Equal(t, []byte("goes to the callback"), got.data[buffered])
	require.Zero(t, got.seen[streamed], "a streamed chunk is not delivered a second time")
}

func TestDownloadChunksStream_NoHashes(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.DownloadChunksStream(context.Background(), downloadScope(), nil, newStreamRecorder().stream(), nil))
}

func TestDownloadChunksStream_RetriesTruncatedBody(t *testing.T) {
	h := testChunkHash(0x42)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("a complete chunk body"))
	srv.truncateGets[h.String()] = 1
	c := newDownloadClient(t, srv, fastRetry(3))

	rec := newStreamRecorder(h)
	require.NoError(t, c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{h}, rec.stream(), newDeliveries().sink))
	require.Equal(t, 2, srv.attemptsFor(h.String()))
	require.Equal(t, 2, rec.attempts[h], "the stream sees the broken attempt and the repeat")
	require.Equal(t, []byte("a complete chunk body"), rec.data[h])
}

func TestDownloadChunksStream_RetriesTransientStatus(t *testing.T) {
	h := testChunkHash(0x43)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("payload"))
	srv.failGets[h.String()] = 2
	srv.failGetStatus = http.StatusBadGateway
	c := newDownloadClient(t, srv, fastRetry(3))

	rec := newStreamRecorder(h)
	require.NoError(t, c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{h}, rec.stream(), newDeliveries().sink))
	require.Equal(t, 3, srv.attemptsFor(h.String()))
	require.Equal(t, 1, rec.attempts[h], "a failed status never reaches the stream")
}

func TestDownloadChunksStream_GivesUpOnBrokenTransfers(t *testing.T) {
	h := testChunkHash(0x44)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("a complete chunk body"))
	srv.truncateGets[h.String()] = 10
	c := newDownloadClient(t, srv, fastRetry(2))

	rec := newStreamRecorder(h)
	err := c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{h}, rec.stream(), newDeliveries().sink)
	require.Error(t, err)
	require.Equal(t, 2, srv.attemptsFor(h.String()))
	require.NotContains(t, rec.data, h)
}

func TestDownloadChunksStream_StreamErrorIsNotRetried(t *testing.T) {
	h := testChunkHash(0x45)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("payload"))
	c := newDownloadClient(t, srv, fastRetry(3))

	rec := newStreamRecorder(h)
	rec.writeErr = errors.New("chunk hash mismatch")
	err := c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{h}, rec.stream(), newDeliveries().sink)
	require.ErrorIs(t, err, rec.writeErr)
	require.Equal(t, 1, srv.attemptsFor(h.String()), "content the stream rejects would be the same on a repeat")
}

func TestDownloadChunksStream_RejectsOversizedBody(t *testing.T) {
	limit := chunker.MaxChunkSize()
	for name, length := range map[string]string{
		"content length":      "actual",
		"no content length":   "",
		"huge claimed length": strconv.FormatInt(1<<40, 10),
	} {
		t.Run(name, func(t *testing.T) {
			h := testChunkHash(0x46)
			srv := newChunkTestServer()
			srv.put(h.String(), make([]byte, limit+1))
			srv.getLength = length
			c := newDownloadClient(t, srv, fastRetry(3))

			rec := newStreamRecorder(h)
			err := c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{h}, rec.stream(), newDeliveries().sink)
			require.ErrorIs(t, err, errChunkTooLarge)
			require.Equal(t, 1, srv.attemptsFor(h.String()))
		})
	}
}

func TestDownloadChunksStream_HTTPErrorAndBadURL(t *testing.T) {
	h := testChunkHash(0x47)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("payload"))
	srv.getStatus = http.StatusNotFound
	c := newDownloadClient(t, srv, fastRetry(3))

	rec := newStreamRecorder(h)
	err := c.DownloadChunksStream(context.Background(), downloadScope(), []serverDomain.Hash{h}, rec.stream(), newDeliveries().sink)
	require.Error(t, err)
	require.Zero(t, rec.attempts[h])

	_, _, err = c.streamChunkOnce(context.Background(), rec.stream(), chunkTransfer{hash: h, url: "http://bad\x00url"})
	require.Error(t, err)
}

func TestCountingReader(t *testing.T) {
	broken := errors.New("connection reset")
	r := &countingReader{r: io.MultiReader(io.LimitReader(zeroReader{}, 5), errReader{broken})}
	_, err := io.ReadAll(r)
	require.ErrorIs(t, err, broken)
	require.Equal(t, int64(5), r.n)
	require.ErrorIs(t, r.err, broken)

	clean := &countingReader{r: io.LimitReader(zeroReader{}, 3)}
	_, err = io.ReadAll(clean)
	require.NoError(t, err)
	require.NoError(t, clean.err, "EOF is not a transfer failure")
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
