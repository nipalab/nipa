//go:build unix

package usecase

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadBlob_WorkingRefusesFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Skip("fifo unsupported")
	}
	d := NewDiff(nil, nil, newDiffStub())

	done := make(chan error, 1)
	go func() {
		_, err := readBlob(t, d, root, BlobRequest{Path: "pipe"})
		done <- err
	}()
	select {
	case err := <-done:
		requireCode(t, err, 400)
	case <-time.After(5 * time.Second):
		t.Fatal("ReadBlob blocked on a FIFO")
	}
}
