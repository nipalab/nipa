package grpc

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	defaultUploadWorkers = 16
	minUploadWorkers     = 4
	maxUploadWorkers     = 64
)

const (
	tuneWindowBytes    = 16 << 20
	tuneWindowDuration = 2 * time.Second
	tuneImproveRatio   = 1.05
	tuneDecreaseRatio  = 3
	tuneDecreaseDiv    = 4
	tuneErrorDiv       = 2
)

func clampUploadWorkers(n int) int {
	if n < 1 {
		return defaultUploadWorkers
	}
	if n > maxUploadWorkers {
		return maxUploadWorkers
	}
	return n
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

func newTransferLimiter(limit int) *transferLimiter {
	l := &transferLimiter{
		tokens: make(chan struct{}, maxUploadWorkers),
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
	if limit < 1 {
		limit = 1
	}
	if limit > maxUploadWorkers {
		limit = maxUploadWorkers
	}
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

// concurrencyTuner adapts the transfer limit to observed throughput using an
// additive-increase/multiplicative-decrease policy: throughput improvements
// raise the limit, stalls lower it, and transfer errors halve it.
type concurrencyTuner struct {
	mu          sync.Mutex
	min         int
	max         int
	bestBps     float64
	windowBytes int64
	windowStart time.Time
}

func newConcurrencyTuner(minLimit, maxLimit, start int) *concurrencyTuner {
	return &concurrencyTuner{min: minLimit, max: maxLimit, bestBps: 0, windowStart: time.Time{}}
}

// observe records a completed transfer and returns the limit to apply next.
func (t *concurrencyTuner) observe(currentLimit int, bytes int64, now time.Time, transferErr error) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if transferErr != nil {
		t.windowBytes = 0
		t.windowStart = time.Time{}
		return t.decrease(currentLimit, tuneErrorDiv)
	}
	if t.windowStart.IsZero() {
		t.windowStart = now
	}
	t.windowBytes += bytes
	if t.windowBytes < tuneWindowBytes && now.Sub(t.windowStart) < tuneWindowDuration {
		return currentLimit
	}

	elapsed := now.Sub(t.windowStart).Seconds()
	bps := float64(t.windowBytes) / elapsed
	t.windowBytes = 0
	t.windowStart = now

	if t.bestBps == 0 || bps > t.bestBps*tuneImproveRatio {
		t.bestBps = bps
		return t.increase(currentLimit)
	}
	return t.decrease(currentLimit, tuneDecreaseDiv)
}

func (t *concurrencyTuner) increase(limit int) int {
	step := limit / 8
	if step < 1 {
		step = 1
	}
	limit += step
	if limit > t.max {
		limit = t.max
	}
	return limit
}

func (t *concurrencyTuner) decrease(limit int, div int) int {
	limit -= limit / div
	if limit < t.min {
		limit = t.min
	}
	return limit
}

func defaultHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          maxUploadWorkers,
		MaxIdleConnsPerHost:   maxUploadWorkers,
		MaxConnsPerHost:       maxUploadWorkers,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: transport}
}
