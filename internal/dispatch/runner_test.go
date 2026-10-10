package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunner_Lifecycle(t *testing.T) {
	runner := NewRunner()
	require.False(t, runner.Running())

	started := make(chan struct{})
	loopExited := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	runner.Start(2,
		func() {
			defer workers.Done()
			<-runner.StopCh()
		},
		func() {
			close(started)
			<-runner.StopCh()
			close(loopExited)
		},
	)
	<-started
	require.True(t, runner.Running())

	runner.Start(2, func() {}, func() {}) // no-op while running

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, runner.Stop(ctx))
	<-loopExited

	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-time.After(time.Second):
		t.Fatal("workers did not exit after Stop")
	}
	require.False(t, runner.Running())

	require.NoError(t, runner.Stop(ctx), "a second stop is a no-op")

	restarted := make(chan struct{})
	runner.Start(1, func() {}, func() { close(restarted) })
	select {
	case <-restarted:
		t.Fatal("a stopped runner must not start again")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRunner_StopWithoutStart(t *testing.T) {
	runner := NewRunner()
	require.NoError(t, runner.Stop(context.Background()))
	require.False(t, runner.Running())
}

func TestRunner_StopTimeout(t *testing.T) {
	runner := NewRunner()
	block := make(chan struct{})
	runner.Start(1, func() { <-block }, func() { <-block })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, runner.Stop(ctx), context.DeadlineExceeded)
	close(block)
}
