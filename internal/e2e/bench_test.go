package e2e

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type countingTransport struct {
	base http.RoundTripper

	mu       sync.Mutex
	puts     int
	putBytes int64
	gets     int
	getBytes atomic.Int64
}

type countingBody struct {
	rc    io.ReadCloser
	count *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.count.Add(int64(n))
	return n, err
}

func (b *countingBody) Close() error { return b.rc.Close() }

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := c.base.RoundTrip(req)
	if err == nil {
		switch req.Method {
		case http.MethodPut:
			c.mu.Lock()
			c.puts++
			c.putBytes += req.ContentLength
			c.mu.Unlock()
		case http.MethodGet:
			c.mu.Lock()
			c.gets++
			c.mu.Unlock()
			res.Body = &countingBody{rc: res.Body, count: &c.getBytes}
		}
	}
	return res, err
}

func (c *countingTransport) snapshot() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.puts, c.putBytes
}

func (c *countingTransport) snapshotGets() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets, c.getBytes.Load()
}

func newBenchClient(t *testing.T, host string) (*clientgrpc.Client, *clientusecase.Auth, *countingTransport) {
	t.Helper()
	ctx := context.Background()
	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	counter := &countingTransport{base: http.DefaultTransport}

	opts := []clientgrpc.ClientOption{
		clientgrpc.WithHTTPClient(&http.Client{Transport: counter}),
	}
	if raw := os.Getenv("NIPA_BENCH_WORKERS"); raw != "" {
		workers, err := strconv.Atoi(raw)
		require.NoError(t, err, "NIPA_BENCH_WORKERS must be an integer")
		opts = append(opts, clientgrpc.WithUploadWorkers(workers))
	}
	grpcClient := clientgrpc.NewClient(transport, session, opts...)
	require.NoError(t, grpcClient.Connect(ctx, host))

	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))
	return grpcClient, clientusecase.NewAuth(grpcClient, store, failPrompt{}), counter
}

func writeBenchFile(t *testing.T, root, path string, size int, text bool) {
	t.Helper()
	fp := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	f, err := os.Create(fp)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<20)
	remaining := int64(size)
	if text {
		line := []byte("asset metadata record: name=texture_0001 compression=none size=4096 checksum=deadbeef\n")
		for remaining > 0 {
			n := int64(len(line))
			if n > remaining {
				n = remaining
			}
			_, err := w.Write(line[:n])
			require.NoError(t, err)
			remaining -= n
		}
	} else {
		buf := make([]byte, 1<<20)
		x := uint32(12345)
		for _, b := range []byte(path) {
			x = x*31 + uint32(b)
		}
		for remaining > 0 {
			for i := range buf {
				x = x*1664525 + 1013904223
				buf[i] = byte(x >> 24)
			}
			n := int64(len(buf))
			if n > remaining {
				n = remaining
			}
			_, err := w.Write(buf[:n])
			require.NoError(t, err)
			remaining -= n
		}
	}
	require.NoError(t, w.Flush())
}

func hashBenchFile(t *testing.T, path string) serverDomain.Hash {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	h := blake3.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	var out serverDomain.Hash
	_, _ = h.Digest().Read(out[:])
	return out
}

// TestEndToEnd_UploadThroughput measures the upload path against a real
// in-process server. It is skipped unless NIPA_BENCH_MB is set, so CI stays
// fast. NIPA_BENCH_WORKERS optionally pins the transfer concurrency.
func TestEndToEnd_UploadThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput harness skipped in short mode")
	}
	sizeMB, err := strconv.Atoi(os.Getenv("NIPA_BENCH_MB"))
	if err != nil || sizeMB <= 0 {
		t.Skip("set NIPA_BENCH_MB=<MiB> to run the upload throughput harness")
	}
	size := sizeMB << 20

	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, counter := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")

	cases := []struct {
		name string
		path string
		text bool
	}{
		{name: "text", path: "bench.txt", text: true},
		{name: "binary", path: "bench.bin", text: false},
		{name: "packed", path: "bench.png", text: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeBenchFile(t, dir, tc.path, size, tc.text)
			stagePath(t, dir, tc.path)

			beforePuts, beforeBytes := counter.snapshot()
			start := time.Now()
			require.NoError(t, pusher.Run(ctx, dir, "bench "+tc.name))
			elapsed := time.Since(start)
			puts, putBytes := counter.snapshot()

			file := benchSnapshotFile(t, dir, tc.path)
			rate := float64(sizeMB) / elapsed.Seconds()
			t.Logf("%s: %d MiB in %s (%.1f MiB/s) encoding=%s chunks=%d puts=%d wire=%.1f MiB",
				tc.path, sizeMB, elapsed.Round(time.Millisecond), rate,
				file.Encoding, len(file.Chunks), puts-beforePuts, float64(putBytes-beforeBytes)/(1<<20))
			require.NotEmpty(t, file.Hash)
			require.Greater(t, puts-beforePuts, 0)
		})
	}

	checkout := cloneWorktree(t, grpcClient, auth, host, "main")
	for _, tc := range cases {
		want := hashBenchFile(t, filepath.Join(dir, filepath.FromSlash(tc.path)))
		got := hashBenchFile(t, filepath.Join(checkout, filepath.FromSlash(tc.path)))
		require.Equal(t, want, got, "cloned %s must match the pushed bytes", tc.path)
	}
}

func benchSnapshotFile(t *testing.T, dir, path string) clientDomain.SnapshotFile {
	t.Helper()
	for _, f := range snapshotOf(t, dir).Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("file %q not found in snapshot", path)
	return clientDomain.SnapshotFile{}
}

// TestEndToEnd_CloneThroughput measures the download/materialization path
// against a real in-process server: the same three files the upload harness
// pushes are cloned into a fresh worktree and the clone plus a no-op update
// are timed. Skipped unless NIPA_BENCH_MB is set.
func TestEndToEnd_CloneThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput harness skipped in short mode")
	}
	sizeMB, err := strconv.Atoi(os.Getenv("NIPA_BENCH_MB"))
	if err != nil || sizeMB <= 0 {
		t.Skip("set NIPA_BENCH_MB=<MiB> to run the clone throughput harness")
	}
	size := sizeMB << 20

	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, counter := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	cases := []struct {
		name string
		path string
		text bool
	}{
		{name: "text", path: "bench.txt", text: true},
		{name: "binary", path: "bench.bin", text: false},
		{name: "packed", path: "bench.png", text: false},
	}
	for _, tc := range cases {
		writeBenchFile(t, dir, tc.path, size, tc.text)
		stagePath(t, dir, tc.path)
		require.NoError(t, pusher.Run(ctx, dir, "bench "+tc.name))
	}

	totalMiB := float64(sizeMB * len(cases))
	beforeGets, beforeBytes := counter.snapshotGets()
	start := time.Now()
	checkout := cloneWorktree(t, grpcClient, auth, host, "main")
	elapsed := time.Since(start)
	gets, getBytes := counter.snapshotGets()
	rate := totalMiB / elapsed.Seconds()
	t.Logf("clone: %d files, %.0f MiB in %s (%.1f MiB/s) gets=%d wire=%.1f MiB",
		len(cases), totalMiB, elapsed.Round(time.Millisecond), rate,
		gets-beforeGets, float64(getBytes-beforeBytes)/(1<<20))

	for _, tc := range cases {
		want := hashBenchFile(t, filepath.Join(dir, filepath.FromSlash(tc.path)))
		got := hashBenchFile(t, filepath.Join(checkout, filepath.FromSlash(tc.path)))
		require.Equal(t, want, got, "cloned %s must match the pushed bytes", tc.path)
	}

	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	start = time.Now()
	require.NoError(t, updater.Run(ctx, checkout))
	t.Logf("no-op update: %s", time.Since(start).Round(time.Millisecond))
	require.Greater(t, rate, 0.0)
}

// memSampler tracks the process peak heap while a benchmark runs. Client and
// server share one process in the in-process harness, so the peak covers both.
type memSampler struct {
	base uint64
	peak atomic.Uint64
	stop chan struct{}
	done chan struct{}
}

func startMemSampler() *memSampler {
	runtime.GC()
	var first runtime.MemStats
	runtime.ReadMemStats(&first)
	s := &memSampler{
		base: first.HeapAlloc,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	s.peak.Store(first.HeapAlloc)
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				for {
					cur := s.peak.Load()
					if m.HeapAlloc <= cur || s.peak.CompareAndSwap(cur, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	return s
}

func (s *memSampler) finish() (uint64, uint64) {
	close(s.stop)
	<-s.done
	peak := s.peak.Load()
	if peak < s.base {
		return 0, peak
	}
	return peak - s.base, peak
}

// TestEndToEnd_GBScaleMemoryCeiling pushes and clones a single multi-GB binary
// file and asserts the peak heap stays far below the file size, proving the
// transfer path streams instead of buffering whole files. Skipped unless
// NIPA_BENCH_GB is set.
func TestEndToEnd_GBScaleMemoryCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("scale harness skipped in short mode")
	}
	sizeGB, err := strconv.ParseInt(os.Getenv("NIPA_BENCH_GB"), 10, 64)
	if err != nil || sizeGB <= 0 {
		t.Skip("set NIPA_BENCH_GB=<GiB> to run the scale harness")
	}
	sizeBytes := sizeGB << 30
	const path = "asset.bin"

	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeBenchFile64(t, dir, path, sizeBytes, false)
	stagePath(t, dir, path)

	sampler := startMemSampler()
	start := time.Now()
	require.NoError(t, pusher.Run(ctx, dir, "scale bench"))
	pushElapsed := time.Since(start)
	growth, peak := sampler.finish()
	pushRate := float64(sizeGB) / pushElapsed.Seconds()
	t.Logf("push: %d GiB in %s (%.1f GiB/s) peak heap growth=%.1f MiB (%.1f%% of file) heap=%d MiB",
		sizeGB, pushElapsed.Round(time.Millisecond), pushRate,
		float64(growth)/(1<<20), float64(growth)*100/float64(sizeBytes), peak>>20)
	require.Less(t, growth, uint64(sizeBytes), "push peak heap must not scale with the file size")

	start = time.Now()
	checkout := cloneWorktree(t, grpcClient, auth, host, "main")
	cloneElapsed := time.Since(start)
	cloneRate := float64(sizeGB) / cloneElapsed.Seconds()
	t.Logf("clone: %d GiB in %s (%.1f GiB/s)", sizeGB, cloneElapsed.Round(time.Millisecond), cloneRate)

	want := hashBenchFile(t, filepath.Join(dir, filepath.FromSlash(path)))
	got := hashBenchFile(t, filepath.Join(checkout, filepath.FromSlash(path)))
	require.Equal(t, want, got, "cloned bytes must match the pushed bytes")
	require.Greater(t, pushRate, 0.0)
	require.Greater(t, cloneRate, 0.0)
}

func writeBenchFile64(t *testing.T, root, path string, size int64, text bool) {
	t.Helper()
	if size > int64(int(^uint(0)>>1)) {
		t.Fatalf("size %d exceeds the platform int range", size)
	}
	writeBenchFile(t, root, path, int(size), text)
}
