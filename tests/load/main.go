// Command load runs scenario-based load tests against a running nipa server.
//
// See tests/load/README.md. The runner drives the client usecase graph
// in-process: one client, many worker goroutines, each clone in its own
// temporary worktree, so it can measure latency and wire bytes without
// spawning a CLI process per operation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

type options struct {
	host         string
	org          string
	project      string
	baseBranch   string
	scenario     string
	clients      int
	duration     time.Duration
	sizeMB       int
	files        int
	user         string
	pass         string
	metricsURL   string
	jsonOutput   bool
	maxErrorRate float64
	keepState    bool
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.host, "host", envOr("NIPA_LOAD_HOST", "127.0.0.1:6745"), "server host:port")
	flag.StringVar(&opts.org, "org", envOr("NIPA_LOAD_ORG", "default"), "org slug")
	flag.StringVar(&opts.project, "project", envOr("NIPA_LOAD_PROJECT", "default"), "project slug")
	flag.StringVar(&opts.baseBranch, "base-branch", "main", "branch scenarios fork from")
	flag.StringVar(&opts.scenario, "scenario", "push", "push | clone | mixed")
	flag.IntVar(&opts.clients, "clients", 8, "concurrent worker worktrees")
	flag.DurationVar(&opts.duration, "duration", 30*time.Second, "run length")
	flag.IntVar(&opts.sizeMB, "size-mb", 8, "MiB written per file and push round")
	flag.IntVar(&opts.files, "files", 4, "files per push round")
	flag.StringVar(&opts.user, "user", envOr("NIPA_LOAD_USER", "nipa"), "login user")
	flag.StringVar(&opts.pass, "pass", envOr("NIPA_LOAD_PASS", "nipa"), "login password")
	flag.StringVar(&opts.metricsURL, "metrics", "", "prometheus endpoint to scrape (default http://host/metrics, off to disable)")
	flag.BoolVar(&opts.jsonOutput, "json", false, "print a JSON summary")
	flag.Float64Var(&opts.maxErrorRate, "max-error-rate", 0.05, "fail when the error rate exceeds this fraction")
	flag.BoolVar(&opts.keepState, "keep-state", false, "keep worktrees and branches for inspection")
	flag.Parse()
	return opts
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type memoryStore struct {
	mu   sync.Mutex
	data map[string]*clientDomain.LoginResult
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: map[string]*clientDomain.LoginResult{}}
}

func (s *memoryStore) SaveToken(data *clientDomain.LoginResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[data.Host] = data
	return nil
}

func (s *memoryStore) LoadToken(host string) (*clientDomain.LoginResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.data[host]; ok {
		return data, nil
	}
	return nil, errors.New("no token stored for host " + host)
}

type noPrompt struct{}

func (noPrompt) PromptUsernameAndPassword() (string, string, error) {
	return "", "", errors.New("unexpected interactive prompt")
}

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
		case http.MethodPut, http.MethodPost:
			c.mu.Lock()
			c.puts++
			if req.ContentLength > 0 {
				c.putBytes += req.ContentLength
			}
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

func (c *countingTransport) wireBytes() (int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.putBytes, c.getBytes.Load()
}

type opStats struct {
	mu      sync.Mutex
	count   int
	errs    int
	samples []time.Duration
}

type collector struct {
	mu  sync.Mutex
	ops map[string]*opStats
}

func newCollector() *collector {
	return &collector{ops: map[string]*opStats{}}
}

func (c *collector) record(name string, d time.Duration, err error) {
	c.mu.Lock()
	s, ok := c.ops[name]
	if !ok {
		s = &opStats{}
		c.ops[name] = s
	}
	c.mu.Unlock()

	s.mu.Lock()
	s.count++
	if err != nil {
		s.errs++
	}
	if len(s.samples) < 200000 {
		s.samples = append(s.samples, d)
	}
	s.mu.Unlock()
}

type opSummary struct {
	Count  int     `json:"count"`
	Errors int     `json:"errors"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
}

func (c *collector) summaries() map[string]opSummary {
	out := map[string]opSummary{}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, s := range c.ops {
		s.mu.Lock()
		sorted := append([]time.Duration(nil), s.samples...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		sum := opSummary{Count: s.count, Errors: s.errs}
		sum.P50Ms = ms(percentile(sorted, 50))
		sum.P95Ms = ms(percentile(sorted, 95))
		sum.P99Ms = ms(percentile(sorted, 99))
		s.mu.Unlock()
		out[name] = sum
	}
	return out
}

func ms(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*10) / 10
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

type summary struct {
	Scenario  string               `json:"scenario"`
	Clients   int                  `json:"clients"`
	DurationS float64              `json:"duration_s"`
	WriteMiB  float64              `json:"write_mib"`
	ReadMiB   float64              `json:"read_mib"`
	WriteMiBs float64              `json:"write_mib_s"`
	ReadMiBs  float64              `json:"read_mib_s"`
	Ops       map[string]opSummary `json:"ops"`
	Metrics   map[string]float64   `json:"metrics_delta,omitempty"`
	ErrorRate float64              `json:"error_rate"`
	TotalOps  int                  `json:"total_ops"`
	TotalErrs int                  `json:"total_errors"`
	WorkerErr int                  `json:"worker_errors"`
}

func run(opts options) error {
	switch opts.scenario {
	case "push", "clone", "mixed":
	default:
		return fmt.Errorf("unknown scenario %q (expected push, clone or mixed)", opts.scenario)
	}
	if opts.clients <= 0 {
		return errors.New("clients must be positive")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, opts.duration)
	defer cancel()

	metricsOff := opts.metricsURL == "off"
	metricsURL := opts.metricsURL
	if metricsURL == "" {
		metricsURL = "http://" + opts.host + "/metrics"
	}
	var beforeMetrics map[string]float64
	if !metricsOff {
		beforeMetrics = scrapeMetrics(metricsURL)
	}

	grpcClient, auth, counter, err := newClient(ctx, opts)
	if err != nil {
		return err
	}

	col := newCollector()
	runID := time.Now().UnixNano()
	seedBranch := fmt.Sprintf("load-%d-seed", runID)

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + opts.host + "/" + opts.org + "/" + opts.project

	seedDir, err := os.MkdirTemp("", "nipa-load-seed-*")
	if err != nil {
		return err
	}
	defer func() {
		if !opts.keepState {
			_ = os.RemoveAll(seedDir)
		}
	}()
	if _, err := grpcClient.CreateBranch(ctx, opts.org, opts.project, seedBranch, opts.baseBranch, "", ""); err != nil {
		return fmt.Errorf("create seed branch: %w", err)
	}
	if err := repo.Clone(ctx, url, opts.host, opts.org, opts.project, seedBranch, nil, seedDir); err != nil {
		return fmt.Errorf("clone seed branch: %w", err)
	}
	writeRandomFile(seedDir, "seed.bin", opts.sizeMB, 1)
	if err := stageFile(seedDir, "seed.bin"); err != nil {
		return err
	}
	if err := pusher.Run(ctx, seedDir, fmt.Sprintf("load seed %d", runID)); err != nil {
		return fmt.Errorf("seed push: %w", err)
	}

	branches := make([]string, 0, opts.clients)
	for i := 0; i < opts.clients; i++ {
		name := fmt.Sprintf("load-%d-%d", runID, i)
		if _, err := grpcClient.CreateBranch(ctx, opts.org, opts.project, name, seedBranch, "", ""); err != nil {
			return fmt.Errorf("create worker branch %s: %w", name, err)
		}
		branches = append(branches, name)
	}
	defer func() {
		if opts.keepState {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, name := range branches {
			_ = grpcClient.DeleteBranch(cleanupCtx, opts.org, opts.project, name)
		}
		_ = grpcClient.DeleteBranch(cleanupCtx, opts.org, opts.project, seedBranch)
	}()

	start := time.Now()
	var wg sync.WaitGroup
	errCh := make(chan error, opts.clients)
	for i := 0; i < opts.clients; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			var werr error
			switch opts.scenario {
			case "push":
				werr = pushLoop(ctx, opts, col, auth, grpcClient, url, branches[worker], worker)
			case "clone":
				werr = cloneLoop(ctx, opts, col, auth, grpcClient, url, branches[worker])
			case "mixed":
				if worker%2 == 0 {
					werr = pushLoop(ctx, opts, col, auth, grpcClient, url, branches[worker], worker)
				} else {
					werr = readLoop(ctx, opts, col, auth, grpcClient, url, branches[worker])
				}
			}
			if werr != nil {
				errCh <- fmt.Errorf("worker %d: %w", worker, werr)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	elapsed := time.Since(start)

	var workerErrs []error
	for werr := range errCh {
		workerErrs = append(workerErrs, werr)
	}

	writeBytes, readBytes := counter.wireBytes()
	sum := summary{
		Scenario:  opts.scenario,
		Clients:   opts.clients,
		DurationS: math.Round(elapsed.Seconds()*10) / 10,
		WriteMiB:  float64(writeBytes) / (1 << 20),
		ReadMiB:   float64(readBytes) / (1 << 20),
		WriteMiBs: float64(writeBytes) / (1 << 20) / elapsed.Seconds(),
		ReadMiBs:  float64(readBytes) / (1 << 20) / elapsed.Seconds(),
		Ops:       col.summaries(),
	}
	for _, s := range sum.Ops {
		sum.TotalOps += s.Count
		sum.TotalErrs += s.Errors
	}
	sum.WorkerErr = len(workerErrs)
	if sum.TotalOps > 0 {
		sum.ErrorRate = math.Round(float64(sum.TotalErrs)/float64(sum.TotalOps)*10000) / 10000
	}
	if !metricsOff {
		afterMetrics := scrapeMetrics(metricsURL)
		sum.Metrics = metricDelta(beforeMetrics, afterMetrics)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sum)
	} else {
		printSummary(sum)
	}

	for _, werr := range workerErrs {
		fmt.Fprintf(os.Stderr, "load: %v\n", werr)
	}
	if sum.TotalOps == 0 {
		return errors.New("no operations completed")
	}
	if sum.WorkerErr > 0 {
		return fmt.Errorf("%d worker(s) failed; see stderr", sum.WorkerErr)
	}
	if sum.ErrorRate > opts.maxErrorRate {
		return fmt.Errorf("error rate %.2f%% exceeds maximum %.2f%%", sum.ErrorRate*100, opts.maxErrorRate*100)
	}
	return nil
}

func newClient(ctx context.Context, opts options) (*clientgrpc.Client, *clientusecase.Auth, *countingTransport, error) {
	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, noPrompt{})
	counter := &countingTransport{base: http.DefaultTransport}
	grpcClient := clientgrpc.NewClient(transport, session, clientgrpc.WithHTTPClient(&http.Client{Transport: counter}))
	if err := grpcClient.Connect(ctx, opts.host); err != nil {
		return nil, nil, nil, fmt.Errorf("connect: %w", err)
	}
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, opts.host, opts.user, opts.pass)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("login: %w", err)
	}
	if err := store.SaveToken(loginResult); err != nil {
		return nil, nil, nil, err
	}
	return grpcClient, clientusecase.NewAuth(grpcClient, store, noPrompt{}), counter, nil
}

func pushLoop(ctx context.Context, opts options, col *collector, auth *clientusecase.Auth, grpcClient *clientgrpc.Client, url, branch string, worker int) error {
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	dir, err := os.MkdirTemp("", fmt.Sprintf("nipa-load-push-%d-*", worker))
	if err != nil {
		return err
	}
	defer func() {
		if !opts.keepState {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := repo.Clone(ctx, url, opts.host, opts.org, opts.project, branch, nil, dir); err != nil {
		return fmt.Errorf("clone %s: %w", branch, err)
	}

	round := 0
	for ctx.Err() == nil {
		round++
		for i := 0; i < opts.files; i++ {
			name := fmt.Sprintf("worker%d/round%d/file%d.bin", worker, round, i)
			writeRandomFile(dir, name, opts.sizeMB, int64(worker)*1000+int64(round)*100+int64(i))
			if err := stageFile(dir, name); err != nil {
				return err
			}
		}
		opStart := time.Now()
		err := pusher.Run(ctx, dir, fmt.Sprintf("load %s worker %d round %d", branch, worker, round))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			col.record("push", time.Since(opStart), err)
			return err
		}
		col.record("push", time.Since(opStart), nil)
	}
	return nil
}

func cloneLoop(ctx context.Context, opts options, col *collector, auth *clientusecase.Auth, grpcClient *clientgrpc.Client, url, branch string) error {
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	for ctx.Err() == nil {
		dir, err := os.MkdirTemp("", "nipa-load-clone-*")
		if err != nil {
			return err
		}
		opStart := time.Now()
		err = repo.Clone(ctx, url, opts.host, opts.org, opts.project, branch, nil, dir)
		if err != nil {
			_ = os.RemoveAll(dir)
			if ctx.Err() != nil {
				return nil
			}
			col.record("clone", time.Since(opStart), err)
			return err
		}
		col.record("clone", time.Since(opStart), nil)
		if !opts.keepState {
			_ = os.RemoveAll(dir)
		}
	}
	return nil
}

func readLoop(ctx context.Context, opts options, col *collector, auth *clientusecase.Auth, grpcClient *clientgrpc.Client, url, branch string) error {
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	dir, err := os.MkdirTemp("", "nipa-load-read-*")
	if err != nil {
		return err
	}
	defer func() {
		if !opts.keepState {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := repo.Clone(ctx, url, opts.host, opts.org, opts.project, branch, nil, dir); err != nil {
		return fmt.Errorf("clone %s: %w", branch, err)
	}

	for ctx.Err() == nil {
		opStart := time.Now()
		_, err := grpcClient.GetCommitLog(ctx, opts.org, opts.project, branch, nil, 50)
		if err != nil && ctx.Err() != nil {
			return nil
		}
		col.record("log", time.Since(opStart), err)
		if err != nil {
			return err
		}

		opStart = time.Now()
		_, err = grpcClient.GetTreeNodeManifest(ctx, opts.org, opts.project, branch, nil)
		if err != nil && ctx.Err() != nil {
			return nil
		}
		col.record("manifest", time.Since(opStart), err)
		if err != nil {
			return err
		}

		opStart = time.Now()
		_, err = grpcClient.ListBranches(ctx, opts.org, opts.project)
		if err != nil && ctx.Err() != nil {
			return nil
		}
		col.record("branches", time.Since(opStart), err)
		if err != nil {
			return err
		}

		opStart = time.Now()
		err = updater.Run(ctx, dir)
		if err != nil && ctx.Err() != nil {
			return nil
		}
		col.record("update-noop", time.Since(opStart), err)
		if err != nil {
			return err
		}
	}
	return nil
}

func stageFile(root, path string) error {
	lr := localrepo.NewLocalRepo()
	if err := lr.Init(root); err != nil {
		return err
	}
	defer lr.Close()
	return lr.StageAdd(path)
}

func writeRandomFile(root, path string, sizeMB int, seed int64) {
	fp := filepath.Join(root, filepath.FromSlash(path))
	_ = os.MkdirAll(filepath.Dir(fp), 0o755)
	f, err := os.Create(fp)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 1<<20)
	x := uint32(seed*2654435761 + 12345)
	remaining := int64(sizeMB) << 20
	for remaining > 0 {
		for i := range buf {
			x = x*1664525 + 1013904223
			buf[i] = byte(x >> 24)
		}
		n := int64(len(buf))
		if n > remaining {
			n = remaining
		}
		if _, err := f.Write(buf[:n]); err != nil {
			return
		}
		remaining -= n
	}
}

func scrapeMetrics(url string) map[string]float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: metrics scrape failed: %v\n", err)
		return nil
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "load: metrics scrape returned %s\n", res.Status)
		return nil
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil
	}
	out := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		nameEnd := strings.IndexAny(line, "{ ")
		if nameEnd <= 0 {
			continue
		}
		name := line[:nameEnd]
		if !strings.HasPrefix(name, "nipa_") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var value float64
		if _, err := fmt.Sscanf(fields[len(fields)-1], "%g", &value); err != nil {
			continue
		}
		out[name] += value
	}
	return out
}

func metricDelta(before, after map[string]float64) map[string]float64 {
	if before == nil || after == nil {
		return nil
	}
	delta := map[string]float64{}
	for name, value := range after {
		d := value - before[name]
		if d != 0 {
			delta[name] = math.Round(d*100) / 100
		}
	}
	return delta
}

func printSummary(sum summary) {
	fmt.Printf("\nscenario=%s clients=%d duration=%.1fs\n", sum.Scenario, sum.Clients, sum.DurationS)
	fmt.Printf("wire: wrote %.1f MiB (%.1f MiB/s), read %.1f MiB (%.1f MiB/s)\n",
		sum.WriteMiB, sum.WriteMiBs, sum.ReadMiB, sum.ReadMiBs)
	fmt.Printf("%-14s %8s %8s %9s %9s %9s\n", "op", "count", "errors", "p50", "p95", "p99")
	names := make([]string, 0, len(sum.Ops))
	for name := range sum.Ops {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := sum.Ops[name]
		fmt.Printf("%-14s %8d %8d %8.1fms %8.1fms %8.1fms\n", name, s.Count, s.Errors, s.P50Ms, s.P95Ms, s.P99Ms)
	}
	fmt.Printf("total: %d ops, %d errors (%.2f%%)\n", sum.TotalOps, sum.TotalErrs, sum.ErrorRate*100)
	if len(sum.Metrics) > 0 {
		fmt.Println("metrics delta (summed across labels):")
		type kv struct {
			name string
			v    float64
		}
		var pairs []kv
		for name, v := range sum.Metrics {
			pairs = append(pairs, kv{name, v})
		}
		sort.Slice(pairs, func(i, j int) bool { return math.Abs(pairs[i].v) > math.Abs(pairs[j].v) })
		for i, p := range pairs {
			if i >= 30 {
				break
			}
			fmt.Printf("  %-55s %+g\n", p.name, p.v)
		}
	}
}
