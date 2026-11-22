package grpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const mib = float64(1 << 20)

// tunerSim drives a tuner with one observation per 2 s window, the bytes of
// which come from a throughput model of the current limit.
type tunerSim struct {
	tun   *concurrencyTuner
	now   time.Time
	limit int
}

func newTunerSim(minLimit, maxLimit, start int) *tunerSim {
	return &tunerSim{tun: newConcurrencyTuner(minLimit, maxLimit), now: time.Unix(1000, 0), limit: start}
}

// window closes one measurement window at bps and returns the next limit.
func (s *tunerSim) window(bps float64) int {
	if s.tun.windowStart.IsZero() {
		s.limit = s.tun.observe(s.limit, 0, s.now, nil)
	}
	s.now = s.now.Add(tuneWindowDuration)
	s.limit = s.tun.observe(s.limit, int64(bps*tuneWindowDuration.Seconds()), s.now, nil)
	return s.limit
}

// run closes n windows with the throughput model and returns the limits.
func (s *tunerSim) run(n int, model func(limit int) float64) []int {
	limits := make([]int, n)
	for i := range limits {
		limits[i] = s.window(model(s.limit))
	}
	return limits
}

func (s *tunerSim) fail() int {
	s.limit = s.tun.observe(s.limit, 0, s.now, errors.New("503"))
	return s.limit
}

// capped is a store that serves perSlot per request in flight up to capacity
// requests: latency-bound below it, saturated above it.
func capped(capacity int, perSlot float64) func(int) float64 {
	return func(limit int) float64 { return float64(min(limit, capacity)) * perSlot }
}

// TestConcurrencyTuner_FlatThroughputHolds is the regression for the walk to
// the minimum: a saturated link whose throughput never changes used to lose
// a quarter of the limit at every window until it sat at 4.
func TestConcurrencyTuner_FlatThroughputHolds(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, defaultUploadWorkers)
	limits := sim.run(100, func(int) float64 { return 100 * mib })
	for _, limit := range limits {
		require.GreaterOrEqual(t, limit, defaultUploadWorkers, "flat throughput must never lower the limit")
		require.LessOrEqual(t, limit, defaultUploadWorkers+2, "a probe that brings nothing is undone")
	}
	held := 0
	for _, limit := range limits {
		if limit == defaultUploadWorkers {
			held++
		}
	}
	require.Greater(t, held, len(limits)*8/10, "the limit holds between probes")
}

func TestConcurrencyTuner_ReachesAndHoldsPlateau(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, defaultUploadWorkers)
	limits := sim.run(60, capped(40, 4*mib))
	for _, limit := range limits[20:] {
		require.GreaterOrEqual(t, limit, 40, "the limit settles at the store's capacity")
		require.LessOrEqual(t, limit, 51, "and only probes one step beyond it")
	}
}

func TestConcurrencyTuner_ClimbsWhileThroughputImproves(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, defaultUploadWorkers)
	limits := sim.run(20, capped(1000, 4*mib))
	require.Equal(t, 18, limits[0], "the first window probes one step up")
	require.Equal(t, maxUploadWorkers, limits[len(limits)-1])
	for i := 1; i < len(limits); i++ {
		require.GreaterOrEqual(t, limits[i], limits[i-1])
	}
}

func TestConcurrencyTuner_ErrorsHalveOncePerCooldown(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, 32)
	require.Equal(t, 16, sim.fail())
	require.Equal(t, 16, sim.fail(), "failures of requests that were in flight together cut once")
	sim.now = sim.now.Add(tuneErrorCooldown)
	require.Equal(t, 8, sim.fail())
	sim.now = sim.now.Add(tuneErrorCooldown)
	require.Equal(t, 4, sim.fail())
	sim.now = sim.now.Add(tuneErrorCooldown)
	require.Equal(t, minUploadWorkers, sim.fail(), "never below the minimum")
}

func TestConcurrencyTuner_RecoversAfterErrors(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, 64)
	model := capped(1000, 4*mib)
	sim.run(3, model)
	require.Equal(t, 32, sim.fail())
	sim.run(12, model)
	require.Equal(t, maxUploadWorkers, sim.limit, "a latency-bound store climbs back after the cut")
}

func TestConcurrencyTuner_RealDropCutsThenRecovers(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, defaultUploadWorkers)
	sim.run(30, capped(40, 4*mib))
	before := sim.limit
	require.GreaterOrEqual(t, before, 40)

	// The store slows down to half its per-request speed: below 70%.
	after := sim.window(capped(40, 2*mib)(sim.limit))
	require.Equal(t, before-before/4, after, "a real drop cuts a quarter")

	// It recovers: the tuner climbs back to the capacity.
	limits := sim.run(20, capped(40, 4*mib))
	require.GreaterOrEqual(t, limits[len(limits)-1], 40)
}

func TestConcurrencyTuner_ModestDipHolds(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, defaultUploadWorkers)
	sim.run(30, capped(40, 4*mib))
	before := sim.limit
	require.Equal(t, before, sim.window(0.8*capped(40, 4*mib)(sim.limit)), "a 20% dip is noise, not a drop")
}

func TestConcurrencyTuner_BestDecays(t *testing.T) {
	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, 32)
	sim.window(100 * mib) // first window: probe to 36
	sim.window(100 * mib) // flat: probe undone
	require.Equal(t, 32, sim.limit)
	sim.tun.bestBps = 100 * mib // a burst

	for range 3 {
		require.Equal(t, 32, sim.window(75*mib), "75% of the burst is flat: hold")
	}
	require.InDelta(t, 75*mib, sim.tun.bestBps, 1, "the burst decayed away (0.9^3 = 0.73)")
	require.Equal(t, 36, sim.window(80*mib), "a gain over the recent level climbs again, though below the old burst")
}

func TestConcurrencyTuner_Windows(t *testing.T) {
	tun := newConcurrencyTuner(minUploadWorkers, maxUploadWorkers)
	base := time.Unix(1000, 0)
	require.Equal(t, 16, tun.observe(16, 0, base, nil))
	require.Equal(t, 16, tun.observe(16, 100<<20, base.Add(time.Second), nil), "64 MiB in under 2 s keeps measuring")
	require.Equal(t, 18, tun.observe(16, 0, base.Add(tuneWindowDuration), nil), "both bounds reached: the window closes")

	tun = newConcurrencyTuner(minUploadWorkers, maxUploadWorkers)
	require.Equal(t, 16, tun.observe(16, 0, base, nil))
	require.Equal(t, 16, tun.observe(16, 10<<20, base.Add(10*time.Second), nil), "2 s without 64 MiB keeps measuring")
	require.Equal(t, 18, tun.observe(16, 1<<20, base.Add(tuneWindowMaxDuration), nil), "a slow link still closes a window")
}

func TestConcurrencyTuner_ClampsAtMaximum(t *testing.T) {
	tun := newConcurrencyTuner(minUploadWorkers, maxUploadWorkers)
	require.Equal(t, maxUploadWorkers, tun.increase(maxUploadWorkers))
	require.Equal(t, 5, tun.increase(4), "the step is at least one")

	sim := newTunerSim(minUploadWorkers, maxUploadWorkers, maxUploadWorkers)
	limits := sim.run(30, func(int) float64 { return 100 * mib })
	for _, limit := range limits {
		require.Equal(t, maxUploadWorkers, limit)
	}
}

// TestUploadChunks_AutoTunedPushHoldsOnSlowStore pushes through a fake store
// that answers every PUT after a fixed latency and serves at most 48 at once,
// like an object store with a per-request latency and a throughput ceiling.
// Four uploads run at once, like the push uploader's windows. The auto-tuned
// upload limit must climb to 32 or more and stay there once the store
// saturates; the old tuner walked it down to the minimum on this store.
func TestUploadChunks_AutoTunedPushHoldsOnSlowStore(t *testing.T) {
	const (
		latency  = 100 * time.Millisecond
		capacity = 48
		uploads  = 4
		perCall  = 500
	)
	srv := newChunkTestServer()
	slots := make(chan struct{}, capacity)
	srv.hookBeforePut = func() {
		slots <- struct{}{}
		time.Sleep(latency)
		<-slots
	}
	c := newDownloadClient(t, srv, fastRetry(3))
	tun := c.uploads.tuner.Load()
	tun.windowMinBytes, tun.windowMinDur = 0, 3*latency

	var (
		mu      sync.Mutex
		samples []int
		done    atomic.Bool
	)
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for !done.Load() {
			mu.Lock()
			samples = append(samples, c.uploads.limiter.currentLimit())
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var wg sync.WaitGroup
	errs := make([]error, uploads)
	for u := range uploads {
		data := make([]*serverDomain.ChunkData, perCall)
		for i := range data {
			var h serverDomain.Hash
			h[0], h[1], h[2] = byte(u), byte(i>>8), byte(i)
			data[i] = &serverDomain.ChunkData{Hash: h, Data: []byte("chunk payload")}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[u] = c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, data)
		}()
	}
	wg.Wait()
	done.Store(true)
	<-sampled
	for _, err := range errs {
		require.NoError(t, err)
	}

	mu.Lock()
	defer mu.Unlock()
	reached := -1
	for i, limit := range samples {
		if limit >= 32 {
			reached = i
			break
		}
	}
	require.GreaterOrEqual(t, reached, 0, "the limit climbs to 32 or more: %v", samples)
	for _, limit := range samples[reached:] {
		require.GreaterOrEqual(t, limit, 32, "and stays there for the rest of the push: %v", samples)
	}
}
