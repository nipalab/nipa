package usecase

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

const maxMaterializeWorkers = 8

type materializeJob struct {
	file     materializedFile
	prevMode int
}

// materializeAll writes every job's file on a bounded worker pool and returns
// the fingerprints of the files it wrote. The first failure stops the pool.
func materializeAll(ctx context.Context, root string, jobs []materializeJob, openChunk func(domain.Hash) (io.ReadCloser, error)) (map[string]clientDomain.StatEntry, error) {
	entries := make(map[string]clientDomain.StatEntry, len(jobs))
	if len(jobs) == 0 {
		return entries, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	work := make(chan materializeJob)
	workers := min(len(jobs), runtime.GOMAXPROCS(0), maxMaterializeWorkers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				f := job.file
				if err := materializeFile(root, f, job.prevMode, openChunk); err != nil {
					once.Do(func() {
						firstErr = &materializeError{path: f.Path, err: err}
						cancel()
					})
					continue
				}
				if entry, err := statEntryFor(root, f.Path, f.FileHash); err == nil {
					mu.Lock()
					entries[f.Path] = entry
					mu.Unlock()
				}
			}
		}()
	}
feed:
	for _, job := range jobs {
		select {
		case work <- job:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// materializeError names the file a materialization failed on without
// changing the underlying message.
type materializeError struct {
	path string
	err  error
}

func (e *materializeError) Error() string { return e.err.Error() }
func (e *materializeError) Unwrap() error { return e.err }

type chunkBuffers struct {
	stored  []byte
	decoded []byte
}

const maxPooledChunkBuffer = 16 << 20

var chunkBufferPool = sync.Pool{New: func() any { return &chunkBuffers{} }}

// materializeFile writes f over its working file. prevMode is the mode the file
// was tracked with before (0 when unknown): a read-only file that the previous
// tracked mode does not explain is a local lock, so it stays read-only.
func materializeFile(root string, f materializedFile, prevMode int, openChunk func(domain.Hash) (io.ReadCloser, error)) error {
	fp := filepath.Join(root, filepath.FromSlash(f.Path))
	dir := filepath.Dir(fp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nipa-materialize-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := writeChunks(tmp, f, openChunk); err != nil {
		_ = tmp.Close()
		return err
	}
	mode := materializedMode(f.Mode)
	if prevMode != 1 && isReadOnlyFile(fp) {
		mode &^= 0o222
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, fp)
}

// writeChunks streams f's chunks into dst. Raw chunks are copied file to file,
// which lets the kernel copy (or reflink) them without a userspace buffer;
// compressed chunks are decoded through pooled buffers.
func writeChunks(dst *os.File, f materializedFile, openChunk func(domain.Hash) (io.ReadCloser, error)) error {
	bufs := chunkBufferPool.Get().(*chunkBuffers)
	defer func() {
		if cap(bufs.stored) > maxPooledChunkBuffer {
			bufs.stored = nil
		}
		if cap(bufs.decoded) > maxPooledChunkBuffer {
			bufs.decoded = nil
		}
		chunkBufferPool.Put(bufs)
	}()

	var written int64
	for _, h := range f.ChunkHashes {
		rc, err := openChunk(h)
		if err != nil {
			return fmt.Errorf("load chunk %s: %w", h, err)
		}
		n, err := writeChunk(dst, rc, f, h, f.SizeBytes-written, bufs)
		_ = rc.Close()
		if err != nil {
			return err
		}
		written += n
		if written > f.SizeBytes {
			return fmt.Errorf("content length mismatch for %s: got %d want %d", f.Path, written, f.SizeBytes)
		}
	}
	if written != f.SizeBytes {
		return fmt.Errorf("content length mismatch for %s: got %d want %d", f.Path, written, f.SizeBytes)
	}
	return nil
}

func writeChunk(dst *os.File, src io.Reader, f materializedFile, h domain.Hash, remaining int64, bufs *chunkBuffers) (int64, error) {
	if f.Encoding != chunker.EncodingZstd {
		// One byte past the remaining size is enough to report a mismatch.
		return io.Copy(dst, &io.LimitedReader{R: src, N: remaining + 1})
	}
	stored, err := readAllInto(bufs.stored[:0], src)
	bufs.stored = stored
	if err != nil {
		return 0, err
	}
	decoded, err := chunker.DecodeAppend(bufs.decoded[:0], f.Encoding, stored)
	if err != nil {
		return 0, fmt.Errorf("decode chunk %s for %s: %w", h, f.Path, err)
	}
	bufs.decoded = decoded
	n, err := dst.Write(decoded)
	return int64(n), err
}

func readAllInto(buf []byte, r io.Reader) ([]byte, error) {
	if f, ok := r.(*os.File); ok {
		if info, err := f.Stat(); err == nil && int64(cap(buf)) < info.Size()+1 {
			buf = make([]byte, 0, info.Size()+1)
		}
	}
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}
