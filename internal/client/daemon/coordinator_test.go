package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type acquireResult struct {
	release func()
	ahead   int
	err     error
}

func acquireAsync(c *coordinator, ctx context.Context, exclusive bool) <-chan acquireResult {
	out := make(chan acquireResult, 1)
	go func() {
		release, ahead, err := c.Acquire(ctx, exclusive)
		out <- acquireResult{release: release, ahead: ahead, err: err}
	}()
	return out
}

func requireGranted(t *testing.T, ch <-chan acquireResult) acquireResult {
	t.Helper()
	select {
	case res := <-ch:
		require.NoError(t, res.err)
		require.NotNil(t, res.release)
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("acquire was not granted")
		return acquireResult{}
	}
}

func requireBlocked(t *testing.T, ch <-chan acquireResult) {
	t.Helper()
	select {
	case res := <-ch:
		t.Fatalf("acquire was granted while it should wait: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCoordinator_SharedRunsConcurrently(t *testing.T) {
	c := newCoordinator()
	r1, _, err := c.Acquire(context.Background(), false)
	require.NoError(t, err)
	r2, _, err := c.Acquire(context.Background(), false)
	require.NoError(t, err)
	r1()
	r2()
}

func TestCoordinator_ExclusiveBlocksShared(t *testing.T) {
	c := newCoordinator()
	release, _, err := c.Acquire(context.Background(), true)
	require.NoError(t, err)

	waiting := acquireAsync(c, context.Background(), false)
	requireBlocked(t, waiting)

	release()
	requireGranted(t, waiting).release()
}

func TestCoordinator_SharedBlocksExclusive(t *testing.T) {
	c := newCoordinator()
	release, _, err := c.Acquire(context.Background(), false)
	require.NoError(t, err)

	waiting := acquireAsync(c, context.Background(), true)
	requireBlocked(t, waiting)

	release()
	requireGranted(t, waiting).release()
}

func TestCoordinator_ExclusiveFIFO(t *testing.T) {
	c := newCoordinator()
	shared, _, err := c.Acquire(context.Background(), false)
	require.NoError(t, err)

	first := acquireAsync(c, context.Background(), true)
	require.Eventually(t, func() bool { return c.queuedLen() == 1 }, time.Second, time.Millisecond)
	second := acquireAsync(c, context.Background(), true)
	require.Eventually(t, func() bool { return c.queuedLen() == 2 }, time.Second, time.Millisecond)

	shared()
	firstRes := requireGranted(t, first)
	requireBlocked(t, second)
	firstRes.release()
	requireGranted(t, second).release()
}

func TestCoordinator_QueuedExclusiveBlocksNewShared(t *testing.T) {
	c := newCoordinator()
	shared, _, err := c.Acquire(context.Background(), false)
	require.NoError(t, err)

	exclusive := acquireAsync(c, context.Background(), true)
	require.Eventually(t, func() bool { return c.queuedLen() == 1 }, time.Second, time.Millisecond)

	late := acquireAsync(c, context.Background(), false)
	require.Eventually(t, func() bool { return c.queuedLen() == 2 }, time.Second, time.Millisecond)

	shared()
	exclusiveRes := requireGranted(t, exclusive)
	requireBlocked(t, late)
	exclusiveRes.release()
	requireGranted(t, late).release()
}

func TestCoordinator_ContextCancel(t *testing.T) {
	c := newCoordinator()
	release, _, err := c.Acquire(context.Background(), true)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	waiting := acquireAsync(c, ctx, false)
	require.Eventually(t, func() bool { return c.queuedLen() == 1 }, time.Second, time.Millisecond)

	cancel()
	select {
	case res := <-waiting:
		require.ErrorIs(t, res.err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled acquire did not return")
	}
	release()
}

func TestCoordinator_CancelledBeforeAcquire(t *testing.T) {
	c := newCoordinator()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := c.Acquire(ctx, false)
	require.ErrorIs(t, err, context.Canceled, "an already-cancelled context is rejected at dispatch")
	require.Zero(t, c.queuedLen())
}

func TestCoordinator_CancelMiddleWaiter(t *testing.T) {
	c := newCoordinator()
	holder, _, err := c.Acquire(context.Background(), true)
	require.NoError(t, err)

	first := acquireAsync(c, context.Background(), true)
	require.Eventually(t, func() bool { return c.queuedLen() == 1 }, time.Second, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	second := acquireAsync(c, ctx, true)
	require.Eventually(t, func() bool { return c.queuedLen() == 2 }, time.Second, time.Millisecond)

	cancel()
	select {
	case res := <-second:
		require.ErrorIs(t, res.err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter behind the head did not return")
	}
	require.Equal(t, 1, c.queuedLen(), "only the cancelled ticket is removed")

	holder()
	requireGranted(t, first).release()
}

func TestCoordinator_AheadReportsQueuedExclusive(t *testing.T) {
	c := newCoordinator()
	first, ahead, err := c.Acquire(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 0, ahead)

	second := acquireAsync(c, context.Background(), true)
	require.Eventually(t, func() bool { return c.queuedLen() == 1 }, time.Second, time.Millisecond)
	third := acquireAsync(c, context.Background(), true)
	require.Eventually(t, func() bool { return c.queuedLen() == 2 }, time.Second, time.Millisecond)

	first()
	secondRes := requireGranted(t, second)
	require.Equal(t, 1, secondRes.ahead, "the active exclusive is counted")
	secondRes.release()
	thirdRes := requireGranted(t, third)
	require.Equal(t, 2, thirdRes.ahead, "the active and the queued exclusive are counted")
	thirdRes.release()
}
