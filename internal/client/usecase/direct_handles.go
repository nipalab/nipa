package usecase

import (
	"container/list"
	"os"
	"sync"
)

// maxOpenTemps bounds the temp-file handles a directPlan keeps open between
// writes. Handles in use are never closed, so a burst of concurrent writes to
// more files than this goes over it by at most the files being written at
// that moment, which is what opening a file per write used to hold.
var maxOpenTemps = 64

// tempHandles keeps the temp files of a directPlan open across chunk writes
// instead of reopening one per chunk. Writers share a handle and use offset
// writes (WriteAt), so it is safe for the concurrent transfer workers. The
// least recently used idle handle is closed when the bound is reached, a
// file's handle is closed when its last chunk settled, and closeAll runs
// before any temp file is renamed or removed (Windows refuses both on an open
// file).
type tempHandles struct {
	mu     sync.Mutex
	open   map[*directFile]*tempHandle
	idle   *list.List
	failed map[*directFile]error
}

type tempHandle struct {
	file *os.File
	refs int
	elem *list.Element
	// done marks a handle whose file is complete while a writer still holds
	// it; the last release closes it.
	done bool
}

func newTempHandles() *tempHandles {
	return &tempHandles{
		open:   make(map[*directFile]*tempHandle),
		idle:   list.New(),
		failed: make(map[*directFile]error),
	}
}

// acquire returns the open handle of df's temp file, opening it when needed.
// Every acquire is paired with a release.
func (t *tempHandles) acquire(df *directFile) (*os.File, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h, ok := t.open[df]; ok {
		if h.refs == 0 {
			t.idle.Remove(h.elem)
			h.elem = nil
		}
		h.refs++
		return h.file, nil
	}
	for len(t.open) >= maxOpenTemps && t.idle.Len() > 0 {
		t.closeLocked(t.idle.Front().Value.(*directFile))
	}
	f, err := os.OpenFile(df.tmp, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	t.open[df] = &tempHandle{file: f, refs: 1}
	return f, nil
}

func (t *tempHandles) release(df *directFile) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.open[df]
	if !ok {
		return
	}
	if h.refs--; h.refs > 0 {
		return
	}
	if h.done {
		t.closeLocked(df)
		return
	}
	h.elem = t.idle.PushBack(df)
}

func (t *tempHandles) closeLocked(df *directFile) {
	h, ok := t.open[df]
	if !ok {
		return
	}
	if h.elem != nil {
		t.idle.Remove(h.elem)
	}
	delete(t.open, df)
	if err := h.file.Close(); err != nil && t.failed[df] == nil {
		t.failed[df] = err
	}
}

// close closes df's handle, or leaves that to the writer still holding it,
// and reports the first error closing it ever returned, so a deferred write
// error is not lost when a handle was closed to make room.
func (t *tempHandles) close(df *directFile) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h, ok := t.open[df]; ok && h.refs > 0 {
		h.done = true
	} else {
		t.closeLocked(df)
	}
	return t.failed[df]
}

func (t *tempHandles) closeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for df := range t.open {
		t.closeLocked(df)
	}
}
