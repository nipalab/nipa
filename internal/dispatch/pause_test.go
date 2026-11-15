package dispatch

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPauseActive(t *testing.T) {
	var p Pause
	require.False(t, p.Active())
	require.True(t, Pause(func() bool { return true }).Active())
	require.False(t, Pause(func() bool { return false }).Active())
}

func TestPauseWaitReturnsOnResume(t *testing.T) {
	var paused atomic.Bool
	paused.Store(true)
	p := Pause(paused.Load)
	go func() {
		time.Sleep(20 * time.Millisecond)
		paused.Store(false)
	}()
	require.True(t, p.Wait(make(chan struct{}), time.Millisecond))
	require.True(t, Pause(nil).Wait(nil, 0))
}

func TestPauseWaitReturnsOnStop(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	require.False(t, Pause(func() bool { return true }).Wait(stop, 0))
}
