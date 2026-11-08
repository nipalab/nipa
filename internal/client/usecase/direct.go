package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

type pinLister interface {
	ListPins() ([]string, error)
}

func loadPins(lr any) ([]string, error) {
	if l, ok := lr.(pinLister); ok {
		return l.ListPins()
	}
	return nil, nil
}

// directEligible reports whether f can be written straight into its working
// file: raw content the object cache does not keep, with every chunk's offset
// known from the manifest.
func directEligible(f materializedFile, pins []string) bool {
	if cachedInPristine(clientDomain.SnapshotFile{Path: f.Path, Encoding: f.Encoding}, pins) {
		return false
	}
	if len(f.ChunkSizes) != len(f.ChunkHashes) {
		return false
	}
	var total int64
	for _, size := range f.ChunkSizes {
		if size <= 0 {
			return false
		}
		total += size
	}
	return total == f.SizeBytes
}

type directFile struct {
	job       materializeJob
	tmp       string
	remaining atomic.Int64
	done      atomic.Bool
	replaced  atomic.Bool
}

type directTarget struct {
	file   *directFile
	offset int64
	size   int64
}

// directPlan assembles raw files in place: every file gets a preallocated
// temp file next to its target, chunks are written at their offsets as they
// arrive, and finish swaps the files in after the whole sync succeeded.
type directPlan struct {
	root    string
	files   []*directFile
	targets map[domain.Hash][]directTarget

	mu      sync.Mutex
	entries map[string]clientDomain.StatEntry
}

func newDirectPlan(root string, jobs []materializeJob) (*directPlan, error) {
	p := &directPlan{
		root:    root,
		targets: make(map[domain.Hash][]directTarget),
		entries: make(map[string]clientDomain.StatEntry, len(jobs)),
	}
	for _, job := range jobs {
		tmp, err := createSizedTemp(root, job.file)
		if err != nil {
			p.cleanup()
			return nil, &materializeError{path: job.file.Path, err: err}
		}
		df := &directFile{job: job, tmp: tmp}
		df.remaining.Store(int64(len(job.file.ChunkHashes)))
		p.files = append(p.files, df)
		var offset int64
		for i, h := range job.file.ChunkHashes {
			size := job.file.ChunkSizes[i]
			p.targets[h] = append(p.targets[h], directTarget{file: df, offset: offset, size: size})
			offset += size
		}
	}
	for _, df := range p.files {
		if len(df.job.file.ChunkHashes) == 0 {
			if err := p.complete(df); err != nil {
				p.cleanup()
				return nil, err
			}
		}
	}
	return p, nil
}

func createSizedTemp(root string, f materializedFile) (string, error) {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(f.Path)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".nipa-materialize-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	err = tmp.Truncate(f.SizeBytes)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func (p *directPlan) wants(h domain.Hash) bool {
	_, ok := p.targets[h]
	return ok
}

// write places one verified chunk at every offset it occupies. Targets of one
// file are contiguous, so each file is opened once per chunk and closed again.
func (p *directPlan) write(h domain.Hash, data []byte) error {
	targets := p.targets[h]
	for start := 0; start < len(targets); {
		df := targets[start].file
		end := start + 1
		for end < len(targets) && targets[end].file == df {
			end++
		}
		if err := p.writeFile(df, targets[start:end], data); err != nil {
			return &materializeError{path: df.job.file.Path, err: err}
		}
		start = end
	}
	return nil
}

func (p *directPlan) writeFile(df *directFile, targets []directTarget, data []byte) error {
	out, err := os.OpenFile(df.tmp, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	for _, t := range targets {
		if int64(len(data)) != t.size {
			_ = out.Close()
			return fmt.Errorf("content length mismatch for %s: chunk is %d bytes, want %d", df.job.file.Path, len(data), t.size)
		}
		if _, err := out.WriteAt(data, t.offset); err != nil {
			_ = out.Close()
			return err
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if df.remaining.Add(-int64(len(targets))) == 0 {
		return p.complete(df)
	}
	return nil
}

func (p *directPlan) complete(df *directFile) error {
	f := df.job.file
	mode := materializedMode(f.Mode)
	if df.job.prevMode != 1 && isReadOnlyFile(filepath.Join(p.root, filepath.FromSlash(f.Path))) {
		mode &^= 0o222
	}
	if err := os.Chmod(df.tmp, mode); err != nil {
		return &materializeError{path: f.Path, err: err}
	}
	df.done.Store(true)
	return nil
}

// copyLocal writes the chunks the object cache already holds.
func (p *directPlan) copyLocal(ctx context.Context, hashes []domain.Hash, load func(domain.Hash) ([]byte, error)) error {
	return runPool(ctx, hashes, func(h domain.Hash) error {
		data, err := load(h)
		if err != nil {
			return fmt.Errorf("load chunk %s: %w", h, err)
		}
		return p.write(h, data)
	})
}

// finish swaps every assembled file in once the whole sync succeeded, so a
// failed download never leaves part of the new tree in the working copy.
func (p *directPlan) finish(ctx context.Context) error {
	for _, df := range p.files {
		if !df.done.Load() {
			return &materializeError{path: df.job.file.Path, err: fmt.Errorf("incomplete download for %s", df.job.file.Path)}
		}
	}
	return runPool(ctx, p.files, func(df *directFile) error {
		f := df.job.file
		if err := replaceFile(df.tmp, filepath.Join(p.root, filepath.FromSlash(f.Path))); err != nil {
			return &materializeError{path: f.Path, err: err}
		}
		df.replaced.Store(true)
		if entry, err := statEntryFor(p.root, f.Path, f.FileHash); err == nil {
			p.mu.Lock()
			p.entries[f.Path] = entry
			p.mu.Unlock()
		}
		return nil
	})
}

func (p *directPlan) cleanup() {
	for _, df := range p.files {
		if !df.replaced.Load() {
			_ = os.Remove(df.tmp)
		}
	}
}

// runPool calls fn for every item on a bounded worker pool and stops feeding
// work after the first failure.
func runPool[T any](ctx context.Context, items []T, fn func(T) error) error {
	if len(items) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	work := make(chan T)
	for range min(len(items), runtime.GOMAXPROCS(0), maxMaterializeWorkers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if err := fn(item); err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}
		}()
	}
feed:
	for _, item := range items {
		select {
		case work <- item:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
