package grpc

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Upload and download concurrency bounds. Each direction has its own limiter
// and tuner: a clone of many small chunks from a latency-bound store wants far
// more requests in flight than a push, and a push must not inherit a limit the
// tuner lowered for a download (or the other way round).
const (
	defaultUploadWorkers = 16
	minUploadWorkers     = 4
	maxUploadWorkers     = 64

	defaultDownloadWorkers = 64
	minDownloadWorkers     = 4
	maxDownloadWorkers     = 256
)

// Tuner constants. A window is one throughput measurement; the tuner decides
// once per window, so these also set how fast it reacts.
const (
	// tuneWindowBytes and tuneWindowDuration must both be reached before a
	// window closes: 64 MiB keeps one large chunk or a short burst from
	// dominating the sample, 2 s keeps a fast link from deciding on a few
	// milliseconds of data.
	tuneWindowBytes    = 64 << 20
	tuneWindowDuration = 2 * time.Second
	// tuneWindowMaxDuration closes a window on a slow link that cannot move
	// 64 MiB in reasonable time, so the tuner still adapts there.
	tuneWindowMaxDuration = 30 * time.Second
	// tuneImproveRatio is the gain over the best recent window that counts as
	// an improvement worth another step up. One step adds an eighth of the
	// limit (12.5%), so a latency-bound store, whose throughput grows with
	// the requests in flight, clears it comfortably while measurement noise
	// on a saturated link does not.
	tuneImproveRatio = 1.05
	// tuneDropRatio is the share of the best recent window below which the
	// tuner treats a window as a real drop (congestion, a slower store) and
	// steps down. Anything between it and tuneImproveRatio is flat and holds
	// the limit: a saturated link does not get faster with more requests, but
	// it does not get slower either, so flat is no reason to back off.
	tuneDropRatio = 0.70
	// tuneBestDecay is applied to the best window at every flat window, so a
	// past burst cannot pin the reference: a 2x burst is forgotten within
	// about seven windows.
	tuneBestDecay = 0.90
	// tuneProbeEvery is the number of consecutive flat windows after which
	// the tuner tries one step up; a probe that brings no improvement is
	// undone at the next window.
	tuneProbeEvery = 8
	// tuneErrorCooldown ignores further errors for this long after an error
	// cut, so a burst of failures from requests that were all in flight at
	// once cuts the limit once instead of walking it to the minimum.
	tuneErrorCooldown = 2 * time.Second
)

const (
	// defaultStallTimeout is how long a chunk download may receive nothing,
	// or an upload may send nothing, before it is cancelled and retried.
	defaultStallTimeout = 60 * time.Second
	// responseHeaderTimeout bounds the wait for a storage endpoint to answer
	// once a request, upload body included, was sent.
	responseHeaderTimeout = 2 * time.Minute
)

// transferPool is the concurrency control of one transfer direction: the
// limiter gating requests and, unless the limit is pinned, the tuner moving it.
type transferPool struct {
	limiter  *transferLimiter
	tuner    atomic.Pointer[concurrencyTuner]
	start    int
	min, max int
}

func newTransferPool(start, minLimit, maxLimit int) *transferPool {
	p := &transferPool{limiter: newTransferLimiter(start, maxLimit), start: start, min: minLimit, max: maxLimit}
	p.tuner.Store(newConcurrencyTuner(minLimit, maxLimit))
	return p
}

// clamp maps a requested worker count onto the pool's range; zero or less
// means the default start.
func (p *transferPool) clamp(n int) int {
	if n < 1 {
		return p.start
	}
	return min(n, p.max)
}

// pin fixes the limit and disables tuning.
func (p *transferPool) pin(workers int) {
	p.tuner.Store(nil)
	p.limiter.setLimit(p.clamp(workers))
}

// record feeds one completed transfer (bytes, nil) or one failed attempt
// (0, err) to the tuner and applies the limit it returns.
func (p *transferPool) record(bytes int64, transferErr error) {
	tuner := p.tuner.Load()
	if tuner == nil {
		return
	}
	limit := p.limiter.currentLimit()
	if next := tuner.observe(limit, bytes, time.Now(), transferErr); next != limit {
		p.limiter.setLimit(next)
	}
}

// transferLimiter gates concurrent chunk transfers behind a limit that can be
// adjusted while transfers are in flight. Workers holding a slot keep it until
// their transfer completes; lowering the limit therefore converges as active
// transfers drain.
type transferLimiter struct {
	mu     sync.Mutex
	tokens chan struct{}
	active int
	limit  int
}

func newTransferLimiter(limit, maxLimit int) *transferLimiter {
	l := &transferLimiter{
		tokens: make(chan struct{}, maxLimit),
		limit:  limit,
	}
	for i := 0; i < limit; i++ {
		l.tokens <- struct{}{}
	}
	return l
}

func (l *transferLimiter) acquire(ctx context.Context) error {
	select {
	case <-l.tokens:
		l.mu.Lock()
		l.active++
		l.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *transferLimiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	if len(l.tokens)+l.active < l.limit {
		l.tokens <- struct{}{}
	}
}

func (l *transferLimiter) setLimit(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	limit = min(max(limit, 1), cap(l.tokens))
	l.limit = limit
	for len(l.tokens)+l.active > limit {
		select {
		case <-l.tokens:
		default:
			return
		}
	}
	for len(l.tokens)+l.active < limit {
		select {
		case l.tokens <- struct{}{}:
		default:
			return
		}
	}
}

func (l *transferLimiter) currentLimit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// concurrencyTuner moves the transfer limit by hill climbing on measured
// throughput:
//
//   - a window more than 5% above the best recent one steps the limit up by an
//     eighth (additive increase, at least one);
//   - a flat window holds the limit, and after tuneProbeEvery flat windows one
//     step up is tried and undone if it does not pay off;
//   - a window below 70% of the best recent one steps the limit down by a
//     quarter, and a retryable transfer failure (network error, 5xx, 408, 429,
//     stall) halves it, at most once per tuneErrorCooldown;
//   - after either cut the reference is cleared: the next window becomes the
//     new baseline and steps up as a probe, which is how the limit recovers.
//
// The best window decays while throughput is flat so an old burst cannot keep
// every later window looking like a drop.
type concurrencyTuner struct {
	mu       sync.Mutex
	min, max int
	// The window bounds: the package constants, shortened in tests.
	windowMinBytes             int64
	windowMinDur, windowMaxDur time.Duration

	bestBps     float64
	bytes       int64
	windowStart time.Time
	flat        int
	// probeFrom is the limit before a probe step; 0 when not probing.
	probeFrom int
	lastCut   time.Time
}

func newConcurrencyTuner(minLimit, maxLimit int) *concurrencyTuner {
	return &concurrencyTuner{
		min:            minLimit,
		max:            maxLimit,
		windowMinBytes: tuneWindowBytes,
		windowMinDur:   tuneWindowDuration,
		windowMaxDur:   tuneWindowMaxDuration,
	}
}

// observe records a completed transfer, or a failed attempt when transferErr
// is set, and returns the limit to apply next.
func (t *concurrencyTuner) observe(limit int, bytes int64, now time.Time, transferErr error) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if transferErr != nil {
		if !t.lastCut.IsZero() && now.Sub(t.lastCut) < tuneErrorCooldown {
			return limit
		}
		return t.cut(limit, limit/2, now)
	}
	if t.windowStart.IsZero() {
		t.windowStart = now
	}
	t.bytes += bytes
	elapsed := now.Sub(t.windowStart)
	if elapsed < t.windowMaxDur && (elapsed < t.windowMinDur || t.bytes < t.windowMinBytes) {
		return limit
	}
	bps := float64(t.bytes) / elapsed.Seconds()
	t.bytes, t.windowStart = 0, now

	switch {
	case t.bestBps == 0 || bps > t.bestBps*tuneImproveRatio:
		// First window after a start or a cut, or a real gain: keep
		// climbing. A first window has no reference yet, so the step is a
		// probe that the next window confirms or undoes.
		t.probeFrom = 0
		if t.bestBps == 0 {
			t.probeFrom = limit
		}
		t.bestBps, t.flat = bps, 0
		return t.increase(limit)
	case bps < t.bestBps*tuneDropRatio:
		return t.cut(limit, limit-limit/4, now)
	}
	// Flat.
	t.bestBps = max(bps, t.bestBps*tuneBestDecay)
	if t.probeFrom > 0 {
		previous := t.probeFrom
		t.probeFrom = 0
		return previous
	}
	t.flat++
	if t.flat < tuneProbeEvery || limit >= t.max {
		return limit
	}
	t.flat = 0
	t.probeFrom = limit
	return t.increase(limit)
}

// cut lowers the limit to next, clears the reference and restarts the window.
func (t *concurrencyTuner) cut(limit, next int, now time.Time) int {
	t.bestBps = 0
	t.bytes, t.windowStart = 0, time.Time{}
	t.flat, t.probeFrom = 0, 0
	t.lastCut = now
	return max(min(next, limit), t.min)
}

func (t *concurrencyTuner) increase(limit int) int {
	return min(limit+max(limit/8, 1), t.max)
}

// defaultHTTPClient pools enough connections for the larger direction's
// maximum, so the HTTP transport never caps a limit the tuner chose.
func defaultHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          maxDownloadWorkers,
		MaxIdleConnsPerHost:   maxDownloadWorkers,
		MaxConnsPerHost:       maxDownloadWorkers,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: transport, CheckRedirect: checkTransferRedirect}
}
