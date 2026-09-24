package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const nipaDir = ".nipa"

type WorkingCopyRepo interface {
	Init(target string) error
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	LoadMergeState() (*domain.MergeState, error)
	LoadRevertState() (*domain.RevertState, error)
	LoadStatCache() (map[string]domain.StatEntry, error)
	SaveStatEntries(entries map[string]domain.StatEntry) error
	StageAdd(path string) error
	StageRemove(paths []string) error
}

type WorkingCopy struct {
	localRepo WorkingCopyRepo
	root      string
	hashFile  func(path, encoding string) (serverDomain.Hash, error)
}

func NewWorkingCopy(localRepo WorkingCopyRepo, root string) (*WorkingCopy, error) {
	if err := localRepo.Init(root); err != nil {
		return nil, err
	}
	w := &WorkingCopy{localRepo: localRepo, root: root}
	w.hashFile = w.workingFileHash
	return w, nil
}

func (w *WorkingCopy) Add(ctx context.Context, targets []string) error {
	paths, err := w.expandAddTargets(targets)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := w.localRepo.StageAdd(path); err != nil {
			return err
		}
	}
	return nil
}

func (w *WorkingCopy) Remove(ctx context.Context, targets []string) error {
	stagedByPath, err := w.listStagedSet()
	if err != nil {
		return err
	}

	removed := make(map[string]bool)
	for _, t := range targets {
		if err := validateRelPath(t); err != nil {
			return err
		}
		if isNipaPath(t) {
			return fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		if t == "" {
			for p := range stagedByPath {
				removed[p] = true
			}
			continue
		}
		if stagedByPath[t] {
			removed[t] = true
			continue
		}
		prefix := t + "/"
		for p := range stagedByPath {
			if strings.HasPrefix(p, prefix) {
				removed[p] = true
			}
		}
	}
	paths := make([]string, 0, len(removed))
	for p := range removed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil
	}
	return w.localRepo.StageRemove(paths)
}

// StatusOptions controls how status verifies working files.
type StatusOptions struct {
	// NoCache rehashes every tracked file instead of trusting fingerprints.
	NoCache bool
}

func (w *WorkingCopy) Status(ctx context.Context, opts ...StatusOptions) (*domain.Status, error) {
	noCache := len(opts) > 0 && opts[0].NoCache

	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := w.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	statCache := map[string]domain.StatEntry{}
	if !noCache {
		statCache, err = w.localRepo.LoadStatCache()
		if err != nil {
			return nil, err
		}
	}
	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}
	stagedByPath := make(map[string]bool, len(staged))
	for _, p := range staged {
		stagedByPath[p] = true
	}

	working, err := walkWorkingFiles(w.root)
	if err != nil {
		return nil, err
	}
	workingSet := make(map[string]bool, len(working))
	for _, p := range working {
		workingSet[p] = true
	}

	st := &domain.Status{Staged: staged}
	mergeState, err := w.localRepo.LoadMergeState()
	if err != nil {
		return nil, err
	}
	if mergeState != nil {
		st.Conflicts = append([]string(nil), mergeState.Conflicts...)
	}
	revertState, err := w.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if revertState != nil {
		st.Conflicts = append(st.Conflicts, revertState.Conflicts...)
	}

	observedAt := time.Now().UnixNano()
	var jobs []statusHashJob
	for _, path := range working {
		if stagedByPath[path] {
			continue
		}
		base, inBase := baseByPath[path]
		if !inBase {
			st.Untracked = append(st.Untracked, path)
			continue
		}
		info, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(path)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				delete(workingSet, path)
				continue
			}
			return nil, err
		}
		entry, cached := statCache[path]
		if !cached || !statMatches(entry, info) {
			jobs = append(jobs, statusHashJob{path: path, encoding: base.Encoding, info: info})
			continue
		}
		if entry.Hash != base.Hash || serverModeFromPerm(info.Mode()) != normalizedMode(base.Mode) {
			st.Modified = append(st.Modified, path)
		}
	}

	updates := make(map[string]domain.StatEntry, len(jobs))
	for i, result := range w.rehash(jobs) {
		job := jobs[i]
		if result.err != nil {
			if errors.Is(result.err, fs.ErrNotExist) {
				delete(workingSet, job.path)
				continue
			}
			return nil, fmt.Errorf("hash %q: %w", job.path, result.err)
		}
		mode := serverModeFromPerm(job.info.Mode())
		updates[job.path] = domain.StatEntry{
			SizeBytes: job.info.Size(),
			MtimeNS:   job.info.ModTime().UnixNano(),
			Mode:      mode,
			Hash:      result.hash,
			CachedAt:  observedAt,
		}
		base := baseByPath[job.path]
		if result.hash != base.Hash || mode != normalizedMode(base.Mode) {
			st.Modified = append(st.Modified, job.path)
		}
	}
	if len(updates) > 0 {
		if err := w.localRepo.SaveStatEntries(updates); err != nil {
			return nil, err
		}
	}

	for _, f := range snapshot.Files {
		if !workingSet[f.Path] {
			st.Missing = append(st.Missing, f.Path)
		}
	}
	sort.Strings(st.Modified)
	return st, nil
}

// statMatches reports whether a cached fingerprint still describes the file.
// The mtime/cached_at comparison guards files written within the same clock
// tick as the hash: those are rehashed once, then cached with a later stamp.
func statMatches(entry domain.StatEntry, info fs.FileInfo) bool {
	mtimeNS := info.ModTime().UnixNano()
	return entry.SizeBytes == info.Size() && entry.MtimeNS == mtimeNS && mtimeNS < entry.CachedAt
}

// normalizedMode treats the proto's unspecified mode as read-write, matching
// how materialization and pushes fall back to 0644.
func normalizedMode(mode int) int {
	if mode == 0 {
		return 2
	}
	return mode
}

type statusHashJob struct {
	path     string
	encoding string
	info     fs.FileInfo
}

type statusHashResult struct {
	hash serverDomain.Hash
	err  error
}

const maxStatusHashWorkers = 8

func statusHashWorkers() int {
	workers := runtime.GOMAXPROCS(0)
	if workers > maxStatusHashWorkers {
		workers = maxStatusHashWorkers
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

// rehash hashes the files whose stat no longer matches the cache, with bounded
// parallelism. Results are indexed so callers can walk them in path order.
func (w *WorkingCopy) rehash(jobs []statusHashJob) []statusHashResult {
	results := make([]statusHashResult, len(jobs))
	if len(jobs) == 0 {
		return results
	}
	hashFile := w.hashFile
	if hashFile == nil {
		hashFile = w.workingFileHash
	}
	workers := statusHashWorkers()
	if workers > len(jobs) {
		workers = len(jobs)
	}
	indices := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range indices {
				job := jobs[idx]
				hash, err := hashFile(job.path, job.encoding)
				results[idx] = statusHashResult{hash: hash, err: err}
			}
		}()
	}
	for i := range jobs {
		indices <- i
	}
	close(indices)
	wg.Wait()
	return results
}

// walkWorkingFiles returns the repo-relative slash paths of regular files in
// the working tree, sorted, skipping the .nipa metadata directory.
func walkWorkingFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if d.Name() == nipaDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func (w *WorkingCopy) listStagedSet() (map[string]bool, error) {
	staged, err := w.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(staged))
	for _, p := range staged {
		set[p] = true
	}
	return set, nil
}

func (w *WorkingCopy) expandAddTargets(targets []string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, t := range targets {
		if err := validateRelPath(t); err != nil {
			return nil, err
		}
		if isNipaPath(t) {
			return nil, fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		abs := w.root
		if t != "" {
			abs = filepath.Join(w.root, filepath.FromSlash(t))
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("path %q does not exist", t)
		}
		if !info.IsDir() {
			if !seen[t] {
				out = append(out, t)
				seen[t] = true
			}
			continue
		}
		err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Name() == nipaDir {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(w.root, p)
			if err != nil {
				return err
			}
			relSlash := filepath.ToSlash(rel)
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if !seen[relSlash] {
				out = append(out, relSlash)
				seen[relSlash] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func (w *WorkingCopy) workingFileHash(path, encoding string) (serverDomain.Hash, error) {
	f, err := os.Open(filepath.Join(w.root, filepath.FromSlash(path)))
	if err != nil {
		return serverDomain.Hash{}, err
	}
	defer func() { _ = f.Close() }()
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return serverDomain.Hash{}, err
	}
	hash, _, _, err := chunkReader(f, path, isBinary, storedEncoding(encoding))
	return hash, err
}

// storedEncoding maps a missing stored encoding to raw so files tracked before
// text compression keep the hashes they were pushed with.
func storedEncoding(encoding string) string {
	if encoding == "" {
		return chunker.EncodingRaw
	}
	return encoding
}

func chunkFile(path string, data []byte, encoding string) (serverDomain.Hash, []serverDomain.Chunk, string, error) {
	return chunkReader(bytes.NewReader(data), path, chunker.IsBinary(data), encoding)
}

func chunkReader(r io.ReadSeeker, path string, isBinary bool, encoding string) (serverDomain.Hash, []serverDomain.Chunk, string, error) {
	var hashes []serverDomain.Hash
	var wrapped []serverDomain.Chunk
	encoding, err := chunker.Encode(r, path, isBinary, encoding, func(c chunker.EncodedChunk) error {
		hashes = append(hashes, c.Hash)
		wrapped = append(wrapped, serverDomain.Chunk{Hash: c.Hash, SizeBytes: c.SizeBytes})
		return nil
	})
	if err != nil {
		return serverDomain.Hash{}, nil, "", err
	}
	return chunker.FileHash(hashes), wrapped, encoding, nil
}

func validateRelPath(t string) error {
	if t == "" {
		return nil
	}
	if filepath.IsAbs(t) {
		return fmt.Errorf("path %q must be relative to the repository root", t)
	}
	clean := filepath.Clean(t)
	if clean == "." || clean == ".." {
		return fmt.Errorf("path %q is outside the repository", t)
	}
	if strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q is outside the repository", t)
	}
	return nil
}

func isNipaPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == nipaDir {
			return true
		}
	}
	return false
}
