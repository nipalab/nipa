package chunkgc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	runs atomic.Int32
	err  error
}

func (f *fakeRunner) Run(context.Context) (Report, error) {
	f.runs.Add(1)
	return Report{}, f.err
}

func TestJob_RunsOnScheduleAndStops(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	job := NewJob(runner, 10*time.Millisecond)
	job.delay = time.Millisecond
	job.Start()
	require.Eventually(t, func() bool { return runner.runs.Load() >= 2 }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, job.Stop(context.Background()))
}

func TestJob_SkipsWhilePaused(t *testing.T) {
	runner := &fakeRunner{}
	job := NewJob(runner, 5*time.Millisecond)
	job.delay = time.Millisecond
	var paused atomic.Bool
	paused.Store(true)
	job.SetPause(paused.Load)
	job.Start()
	time.Sleep(40 * time.Millisecond)
	require.Zero(t, runner.runs.Load())
	paused.Store(false)
	require.Eventually(t, func() bool { return runner.runs.Load() > 0 }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, job.Stop(context.Background()))
}

func TestJob_DefaultsAndStop(t *testing.T) {
	job := NewJob(&fakeRunner{}, 0)
	require.Equal(t, DefaultInterval, job.interval)
	require.NoError(t, job.Stop(context.Background()))

	block := make(chan struct{})
	job = NewJob(blockingRunner(block), time.Hour)
	job.delay = time.Millisecond
	job.Start()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, job.Stop(ctx), context.Canceled)
	close(block)
}

type blockingRunner chan struct{}

func (b blockingRunner) Run(context.Context) (Report, error) {
	<-b
	return Report{}, nil
}
