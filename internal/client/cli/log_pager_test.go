package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// chanReader returns bytes pushed into a channel, one chunk per Read call,
// simulating how a raw terminal delivers each keypress as a separate read.
type chanReader struct {
	ch chan []byte
}

func (r *chanReader) Read(p []byte) (int, error) {
	b, ok := <-r.ch
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func runPagerAsync(t *testing.T, out *bytes.Buffer, in io.Reader, getSize func() (int, int), sigs <-chan os.Signal) (chan error, func()) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- runLogPager(out, in, testEntries(), false, getSize, sigs)
	}()
	return done, func() {}
}

func TestRunLogPager_Quit(t *testing.T) {
	var out bytes.Buffer
	sigs := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runLogPager(&out, strings.NewReader("q"), testEntries(), false,
			func() (int, int) { return 80, 24 }, sigs)
	}()
	require.NoError(t, <-done)
	require.Contains(t, out.String(), "nipa log")
	require.Contains(t, out.String(), "commit ")
}

func TestRunLogPager_ScrollAndQuit(t *testing.T) {
	var out bytes.Buffer
	in := &chanReader{ch: make(chan []byte, 4)}
	sigs := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runLogPager(&out, in, testEntries(), false,
			func() (int, int) { return 80, 24 }, sigs)
	}()
	in.ch <- []byte("j")
	in.ch <- []byte("j")
	in.ch <- []byte("q")
	close(in.ch)
	require.NoError(t, <-done)
	require.Contains(t, out.String(), "nipa log")
}

func TestRunLogPager_Resize(t *testing.T) {
	var out bytes.Buffer
	in := &chanReader{ch: make(chan []byte, 4)}
	sigs := make(chan os.Signal, 1)

	var height atomic.Int32
	height.Store(24)
	sawResize := make(chan struct{})
	getSize := func() (int, int) {
		if height.Load() == 10 {
			select {
			case <-sawResize:
			default:
				close(sawResize)
			}
		}
		return 80, int(height.Load())
	}

	done := make(chan error, 1)
	go func() {
		done <- runLogPager(&out, in, testEntries(), false, getSize, sigs)
	}()
	sigs <- syscall.SIGWINCH
	height.Store(10)
	<-sawResize
	in.ch <- []byte("q")
	close(in.ch)
	require.NoError(t, <-done)
	require.Contains(t, out.String(), "nipa log")
}

func TestRunLogPager_EmptyEntries(t *testing.T) {
	var out bytes.Buffer
	sigs := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runLogPager(&out, strings.NewReader("q"), nil, false,
			func() (int, int) { return 80, 24 }, sigs)
	}()
	require.NoError(t, <-done)
	require.Contains(t, out.String(), "no commits")
}

func TestRunLogPager_InputError(t *testing.T) {
	var out bytes.Buffer
	sigs := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runLogPager(&out, strings.NewReader(""), testEntries(), false,
			func() (int, int) { return 80, 24 }, sigs)
	}()
	require.NoError(t, <-done)
}

func TestDrawLog_Footer(t *testing.T) {
	var out bytes.Buffer
	vp := newLogViewport([]string{"line1", "line2"}, 2)
	drawLog(&out, vp, "footer-bar")
	require.Contains(t, out.String(), "line1")
	require.Contains(t, out.String(), "line2")
	require.Contains(t, out.String(), "footer-bar")
}
