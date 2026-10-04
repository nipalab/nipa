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
	"github.com/nipalab/nipa/internal/client/ignore"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const nipaDir = ".nipa"

type WorkingCopyRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
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

// AddOptions controls how paths are staged.
type AddOptions struct {
	// Force stages paths even when they match the ignore rules.
	Force bool
}

func (w *WorkingCopy) Add(ctx context.Context, targets []string, opts ...AddOptions) error {
	force := len(opts) > 0 && opts[0].Force
	paths, err := w.expandAddTargets(targets, force)
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

	cfg, err := w.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
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
	ignores, err := newIgnoreState(w.root, append(snapshotPaths(snapshot), staged...))
	if err != nil {
		return nil, err
	}

	working, err := walkWorkingFiles(w.root, ignores)
	if err != nil {
		return nil, err
	}
	workingSet := make(map[string]bool, len(working))
	for _, p := range working {
		workingSet[p] = true
	}

	st := &domain.Status{Branch: cfg.Branch, Head: cfg.Head}
	for _, p := range staged {
		_, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(p)))
		switch {
		case err == nil:
			st.Staged = append(st.Staged, p)
		case errors.Is(err, fs.ErrNotExist):
			st.Deleted = append(st.Deleted, p)
		default:
			return nil, err
		}
	}
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
		if !workingSet[f.Path] && !stagedByPath[f.Path] {
			st.Missing = append(st.Missing, f.Path)
		}
	}
	sort.Strings(st.Modified)
	return st, nil
}

// RefreshStatEntries rehashes the given working files and refreshes their stat
// cache fingerprints, so a later Status can trust them without reading. It is
// the daemon watcher's accelerator: paths that left the snapshot or the disk,
// and files whose fingerprint Status would already trust, are skipped — the
// trust model stays exactly Status's, so the accelerator never changes an
// answer.
func (w *WorkingCopy) RefreshStatEntries(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return err
	}
	statCache, err := w.localRepo.LoadStatCache()
	if err != nil {
		return err
	}
	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}

	observedAt := time.Now().UnixNano()
	var jobs []statusHashJob
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		base, tracked := baseByPath[path]
		if !tracked {
			continue
		}
		info, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(path)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if entry, cached := statCache[path]; cached && statMatches(entry, info) {
			continue
		}
		jobs = append(jobs, statusHashJob{path: path, encoding: base.Encoding, info: info})
	}

	updates := make(map[string]domain.StatEntry, len(jobs))
	for i, result := range w.rehash(jobs) {
		job := jobs[i]
		if result.err != nil {
			if errors.Is(result.err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("hash %q: %w", job.path, result.err)
		}
		updates[job.path] = domain.StatEntry{
			SizeBytes: job.info.Size(),
			MtimeNS:   job.info.ModTime().UnixNano(),
			Mode:      serverModeFromPerm(job.info.Mode()),
			Hash:      result.hash,
			CachedAt:  observedAt,
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return w.localRepo.SaveStatEntries(updates)
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

// ignoreState applies the ignore rules to a working-tree walk while keeping
// tracked and staged paths exempt: tracking always wins over ignore rules.
type ignoreState struct {
	matcher   *ignore.Matcher
	protected map[string]bool
}

func newIgnoreState(root string, protected []string) (*ignoreState, error) {
	matcher, err := ignore.New(root)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(protected)*2)
	for _, p := range protected {
		if p == "" {
			continue
		}
		set[p] = true
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			set[p[:i]] = true
		}
	}
	return &ignoreState{matcher: matcher, protected: set}, nil
}

func snapshotPaths(snapshot *domain.Snapshot) []string {
	paths := make([]string, 0, len(snapshot.Files))
	for _, f := range snapshot.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func (s *ignoreState) skipDir(rel string) bool {
	if s == nil || s.matcher.Empty() {
		return false
	}
	return s.matcher.Ignores(rel, true) && !s.protected[rel]
}

func (s *ignoreState) skipFile(rel string) bool {
	if s == nil || s.matcher.Empty() {
		return false
	}
	return s.matcher.Ignores(rel, false) && !s.protected[rel]
}

// walkWorkingFiles returns the repo-relative slash paths of regular files in
// the working tree, sorted, skipping the .nipa metadata directory and ignored
// paths that are neither tracked nor staged.
func walkWorkingFiles(root string, ignores *ignoreState) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == nipaDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			if ignores.skipDir(relSlash) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if ignores.skipFile(relSlash) {
			return nil
		}
		paths = append(paths, relSlash)
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

func (w *WorkingCopy) expandAddTargets(targets []string, force bool) ([]string, error) {
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	ignores, err := newIgnoreState(w.root, snapshotPaths(snapshot))
	if err != nil {
		return nil, err
	}

	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		if !seen[p] {
			out = append(out, p)
			seen[p] = true
		}
	}
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
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			if !stageTrackedTarget(snapshot, t, add) {
				return nil, fmt.Errorf("path %q does not exist", t)
			}
			continue
		}
		if !info.IsDir() {
			if !force && ignores.skipFile(t) {
				return nil, ignoredPathError(t)
			}
			add(t)
			continue
		}
		if !force && ignores.skipDir(t) {
			return nil, ignoredPathError(t)
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
			if rel == "." {
				return nil
			}
			relSlash := filepath.ToSlash(rel)
			if d.IsDir() {
				if !force && ignores.skipDir(relSlash) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if !force && ignores.skipFile(relSlash) {
				return nil
			}
			add(relSlash)
			return nil
		})
		if err != nil {
			return nil, err
		}
		prefix := ""
		if t != "" {
			prefix = t + "/"
		}
		for _, f := range snapshot.Files {
			if strings.HasPrefix(f.Path, prefix) {
				add(f.Path)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func ignoredPathError(path string) error {
	return fmt.Errorf("path %q is ignored; use -f to add it anyway", path)
}

// stageTrackedTarget marks a missing add target as a deletion: the exact path
// when it was a tracked file, otherwise every tracked file under it. It
// reports whether the snapshot still tracks anything at the target.
func stageTrackedTarget(snapshot *domain.Snapshot, target string, add func(string)) bool {
	staged := false
	for _, f := range snapshot.Files {
		if f.Path == target || strings.HasPrefix(f.Path, target+"/") {
			add(f.Path)
			staged = true
		}
	}
	return staged
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
