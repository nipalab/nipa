package usecase

import (
	"bytes"
	"context"
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
	"github.com/nipalab/nipa/internal/provenance"
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
	OpenChunk(hash serverDomain.Hash) (io.ReadCloser, error)
	LoadCommit() (*domain.LocalCommit, error)
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	StoreChunks(chunks []*serverDomain.ChunkData) error
	StageAdd(path string) error
	StageAddMany(paths []string) error
	StageRemove(paths []string) error
}

type WorkingCopy struct {
	localRepo WorkingCopyRepo
	root      string
	// hashFile replaces the working file hasher in tests; nil hashes the file.
	hashFile   func(path, encoding string) (serverDomain.Hash, error)
	auth       *Auth
	downloader ContentDownloader
}

func NewWorkingCopy(localRepo WorkingCopyRepo, root string) (*WorkingCopy, error) {
	if err := localRepo.Init(root); err != nil {
		return nil, err
	}
	return &WorkingCopy{localRepo: localRepo, root: root}, nil
}

// WithDownloader lets content reads fetch snapshot chunks that are missing
// from the local cache and cannot be recaptured from the working file.
func (w *WorkingCopy) WithDownloader(auth *Auth, client ContentDownloader) *WorkingCopy {
	w.auth = auth
	w.downloader = client
	return w
}

func (w *WorkingCopy) pristine() pristine {
	return pristine{root: w.root, repo: w.localRepo, auth: w.auth, client: w.downloader}
}

// AddOptions controls how paths are staged.
type AddOptions struct {
	// Force stages paths even when they match the ignore rules.
	Force bool
	// NoProvenance skips recording provenance for the staged files.
	NoProvenance bool
}

func (w *WorkingCopy) Add(ctx context.Context, targets []string, opts ...AddOptions) error {
	_, err := w.AddWithReport(ctx, targets, opts...)
	return err
}

// AddWithReport stages like Add and reports the provenance records it
// wrote when the repository has a .nipaprovenance.yml.
func (w *WorkingCopy) AddWithReport(ctx context.Context, targets []string, opts ...AddOptions) (ProvenanceReport, error) {
	var opt AddOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	var rep ProvenanceReport
	// The scope reads only the rows the targets name: single rows for files,
	// one range of the snapshot for a directory, everything for the root.
	tracked, err := newTrackedScope(w.localRepo)
	if err != nil {
		return rep, err
	}
	paths, err := w.expandAddTargets(tracked, targets, opt.Force)
	if err != nil {
		return rep, err
	}
	paths, err = w.withoutUnchanged(tracked, paths)
	if err != nil {
		return rep, err
	}
	if !opt.NoProvenance {
		var changed bool
		if rep, changed, err = w.recordProvenance(tracked, paths); err != nil {
			return rep, err
		}
		if changed {
			paths = withProvenanceFile(paths)
		}
	}
	untracked, err := tracked.untracked(paths)
	if err != nil {
		return rep, err
	}
	rep.Generated = ignore.DetectGenerated(w.root, untracked)
	return rep, w.localRepo.StageAddMany(paths)
}

// scopedStatEntries loads the fingerprints of the tracked paths a scope was
// asked about: the rows of those paths when the repository reads by path,
// the whole cache when the scope holds the whole snapshot anyway.
func (w *WorkingCopy) scopedStatEntries(tracked *trackedScope, paths []string) (map[string]domain.StatEntry, error) {
	if getter, ok := w.localRepo.(statEntryGetter); ok && !tracked.all {
		return getter.StatEntries(paths)
	}
	return w.localRepo.LoadStatCache()
}

// withoutUnchanged drops tracked paths whose cached fingerprint still matches
// the snapshot, so adding a large tree only stages new, modified and deleted
// files. Paths without a trustworthy fingerprint stay staged; Add never hashes
// content.
func (w *WorkingCopy) withoutUnchanged(scope *trackedScope, paths []string) ([]string, error) {
	if err := scope.prefetch(paths); err != nil {
		return nil, err
	}
	trackedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, tracked := scope.files[path]; tracked {
			trackedPaths = append(trackedPaths, path)
		}
	}
	statCache, err := w.scopedStatEntries(scope, trackedPaths)
	if err != nil {
		return nil, err
	}
	if len(statCache) == 0 {
		return paths, nil
	}
	baseByPath := scope.files
	kept := paths[:0]
	for _, path := range paths {
		base, tracked := baseByPath[path]
		if !tracked {
			kept = append(kept, path)
			continue
		}
		info, err := statTracked(w.root, path)
		if err != nil {
			if isMissing(err) {
				kept = append(kept, path)
				continue
			}
			return nil, err
		}
		entry, cached := statCache[path]
		if cached && statMatches(entry, info) && entry.Hash == base.Hash && trackedModeFromPerm(info.Mode(), base.Mode) == normalizedMode(base.Mode) {
			continue
		}
		kept = append(kept, path)
	}
	return kept, nil
}

func (w *WorkingCopy) Remove(ctx context.Context, targets []string) error {
	stagedByPath, err := w.listStagedSet()
	if err != nil {
		return err
	}

	removed := make(map[string]bool)
	for _, raw := range targets {
		t, err := normalizeTarget(raw)
		if err != nil {
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
	_, changed, err := w.forgetProvenance(paths)
	if err != nil {
		return err
	}
	if err := w.localRepo.StageRemove(paths); err != nil {
		return err
	}
	if changed {
		return w.localRepo.StageAdd(provenance.FileName)
	}
	return nil
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
		var err error
		if _, tracked := baseByPath[p]; tracked {
			// A tracked file that is a folder now is a deletion.
			_, err = statTracked(w.root, p)
		} else {
			_, err = os.Stat(filepath.Join(w.root, filepath.FromSlash(p)))
		}
		switch {
		case err == nil:
			st.Staged = append(st.Staged, p)
			if _, tracked := baseByPath[p]; !tracked {
				st.Added = append(st.Added, p)
			}
		case isMissing(err):
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
		st.Resolved = append([]string(nil), mergeState.Resolved...)
		st.Pending = "merge"
	}
	revertState, err := w.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if revertState != nil {
		st.Conflicts = append(st.Conflicts, revertState.Conflicts...)
		st.Resolved = append(st.Resolved, revertState.Resolved...)
		st.Pending, st.PendingRemaining = "revert", len(revertState.Targets)
	}
	if l, ok := w.localRepo.(shelfStateLoader); ok {
		shelfState, err := l.LoadShelfState()
		if err != nil {
			return nil, err
		}
		if shelfState != nil {
			st.Conflicts = append(st.Conflicts, shelfState.Conflicts...)
			st.Resolved = append(st.Resolved, shelfState.Resolved...)
			st.Pending = "shelf"
		}
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
		info, err := statTracked(w.root, path)
		if err != nil {
			if isMissing(err) {
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
		if entry.Hash != base.Hash || trackedModeFromPerm(info.Mode(), base.Mode) != normalizedMode(base.Mode) {
			st.Modified = append(st.Modified, path)
		}
	}

	updates := make(map[string]domain.StatEntry, len(jobs))
	for i, result := range w.rehash(jobs) {
		job := jobs[i]
		if result.err != nil {
			if isMissing(result.err) {
				delete(workingSet, job.path)
				continue
			}
			return nil, fmt.Errorf("hash %q: %w", job.path, result.err)
		}
		base := baseByPath[job.path]
		mode := trackedModeFromPerm(job.info.Mode(), base.Mode)
		updates[job.path] = domain.StatEntry{
			SizeBytes: job.info.Size(),
			MtimeNS:   job.info.ModTime().UnixNano(),
			Mode:      mode,
			Hash:      result.hash,
			CachedAt:  observedAt,
		}
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
	scope, err := newTrackedScope(w.localRepo)
	if err != nil {
		return err
	}
	if err := scope.prefetch(paths); err != nil {
		return err
	}
	statCache, err := w.scopedStatEntries(scope, paths)
	if err != nil {
		return err
	}
	baseByPath := scope.files

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
		info, err := statTracked(w.root, path)
		if err != nil {
			if isMissing(err) {
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
			if isMissing(result.err) {
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
	// chunks is the file's chunk layout, kept only when a caller asks for it.
	chunks []serverDomain.Chunk
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
	return w.rehashLayout(jobs, false)
}

// rehashLayout is rehash that can also keep each file's chunk layout (hashes
// and sizes in file order), which the one read produces anyway.
func (w *WorkingCopy) rehashLayout(jobs []statusHashJob, keepLayout bool) []statusHashResult {
	results := make([]statusHashResult, len(jobs))
	if len(jobs) == 0 {
		return results
	}
	hashJob := func(job statusHashJob) statusHashResult {
		if w.hashFile != nil {
			hash, err := w.hashFile(job.path, job.encoding)
			return statusHashResult{hash: hash, err: err}
		}
		hash, chunks, err := hashWorkingLayout(w.root, job.path, job.encoding)
		if !keepLayout {
			chunks = nil
		}
		return statusHashResult{hash: hash, err: err, chunks: chunks}
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
				results[idx] = hashJob(jobs[idx])
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
	// lookup replaces protected when the exempt paths are looked up on
	// demand instead of listed up front.
	lookup func(rel string) bool
}

// newLookupIgnoreState is newIgnoreState with the exemption answered by
// lookup, which is only asked about paths the rules ignore.
func newLookupIgnoreState(root string, lookup func(rel string) bool) (*ignoreState, error) {
	matcher, err := ignore.New(root)
	if err != nil {
		return nil, err
	}
	return &ignoreState{matcher: matcher, lookup: lookup}, nil
}

func (s *ignoreState) exempt(rel string) bool {
	if s.lookup != nil {
		return s.lookup(rel)
	}
	return s.protected[rel]
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
	return s.matcher.Ignores(rel, true) && !s.exempt(rel)
}

func (s *ignoreState) skipFile(rel string) bool {
	if s == nil || s.matcher.Empty() {
		return false
	}
	return s.matcher.Ignores(rel, false) && !s.exempt(rel)
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

func (w *WorkingCopy) expandAddTargets(tracked *trackedScope, targets []string, force bool) ([]string, error) {
	ignores, err := newLookupIgnoreState(w.root, tracked.protects)
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
	for _, raw := range targets {
		t, err := normalizeTarget(raw)
		if err != nil {
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
			if !isMissing(err) {
				return nil, err
			}
			found, err := stageTrackedTarget(tracked, t, add)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("path %q does not exist", t)
			}
			continue
		}
		if !info.IsDir() {
			ignored := !force && ignores.skipFile(t)
			if tracked.err != nil {
				return nil, tracked.err
			}
			if ignored {
				return nil, ignoredPathError(t)
			}
			add(t)
			if err := stageDisplaced(tracked, t, false, add); err != nil {
				return nil, err
			}
			continue
		}
		if err := stageDisplaced(tracked, t, true, add); err != nil {
			return nil, err
		}
		// Loaded before the walk: the tracked files below the directory are
		// both the deletions to stage and the walk's ignore exemptions.
		under, err := tracked.under(t)
		if err != nil {
			return nil, err
		}
		ignored := !force && ignores.skipDir(t)
		if tracked.err != nil {
			return nil, tracked.err
		}
		if ignored {
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
		if tracked.err != nil {
			return nil, tracked.err
		}
		for _, p := range under {
			add(p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func ignoredPathError(path string) error {
	return fmt.Errorf("path %q is ignored; use -f to add it anyway", path)
}

// stageDisplaced stages the deletions an existing add target implies beyond
// itself, the way a missing target stages what was tracked there
// (stageTrackedTarget): a tracked file cannot exist where the working copy
// has a folder, nor below a path that is a file now. So the tracked files
// that are folders on the way to the target (the target included when it is
// a folder) are staged, and so is every tracked file below a target that is
// a file and was not tracked as one. Without them a push of the target alone
// would make the path both a file and a folder, which the server refuses.
//
// It reads the rows of the target and its parent folders in one batch and,
// for a file target that is not tracked, seeks once below it.
func stageDisplaced(tracked *trackedScope, target string, isDir bool, add func(string)) error {
	if target == "" {
		return nil
	}
	var above []string
	for i := strings.IndexByte(target, '/'); i >= 0; {
		above = append(above, target[:i])
		next := strings.IndexByte(target[i+1:], '/')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	// The target's own row rides in the same read: a folder target may be a
	// tracked file, and a file target is looked up next anyway.
	if err := tracked.prefetch(append(above, target)); err != nil {
		return err
	}
	if isDir {
		above = append(above, target)
	}
	for _, p := range above {
		if _, ok := tracked.files[p]; ok {
			add(p)
		}
	}
	if isDir {
		return nil
	}
	if _, ok := tracked.files[target]; ok {
		// A tracked file has no tracked file below it.
		return nil
	}
	// One index seek tells the usual new file apart; only a path that really
	// replaced a folder has its tracked files listed.
	if holds, err := tracked.tracksUnder(target); err != nil || !holds {
		return err
	}
	under, err := tracked.under(target)
	if err != nil {
		return err
	}
	for _, p := range under {
		add(p)
	}
	return nil
}

// stageTrackedTarget marks a missing add target as a deletion: the exact path
// when it was a tracked file, otherwise every tracked file under it. It
// reports whether the snapshot still tracks anything at the target.
func stageTrackedTarget(tracked *trackedScope, target string, add func(string)) (bool, error) {
	if target == "" {
		return false, nil
	}
	_, staged, err := tracked.get(target)
	if err != nil {
		return false, err
	}
	if staged {
		add(target)
	}
	under, err := tracked.under(target)
	if err != nil {
		return false, err
	}
	for _, p := range under {
		add(p)
		staged = true
	}
	return staged, nil
}

func (w *WorkingCopy) workingFileHash(path, encoding string) (serverDomain.Hash, error) {
	return hashWorkingPath(w.root, path, encoding)
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

const inlineContentMax = chunker.TextSingleBlobRawMax

type hashedFile struct {
	hash     serverDomain.Hash
	chunks   []serverDomain.Chunk
	encoding string
	size     int64
	isBinary bool
	inline   []byte
}

// hashWorkingFile hashes a working file without retaining it: files up to
// inlineContentMax are read once and, when text, handed back as inline;
// larger ones are streamed through the chunker.
func hashWorkingFile(abs, rel string, size int64, encoding string) (hashedFile, error) {
	if size <= inlineContentMax {
		data, err := os.ReadFile(abs)
		if err != nil {
			return hashedFile{}, err
		}
		hash, chunks, enc, err := chunkFile(rel, data, encoding)
		if err != nil {
			return hashedFile{}, err
		}
		out := hashedFile{hash: hash, chunks: chunks, encoding: enc, size: int64(len(data)), isBinary: chunker.IsBinary(data)}
		if !out.isBinary {
			out.inline = data
		}
		return out, nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return hashedFile{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return hashedFile{}, err
	}
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return hashedFile{}, err
	}
	hash, chunks, enc, err := chunkReader(f, rel, isBinary, encoding)
	if err != nil {
		return hashedFile{}, err
	}
	return hashedFile{hash: hash, chunks: chunks, encoding: enc, size: info.Size(), isBinary: isBinary}, nil
}

func chunkHashesAndSizes(chunks []serverDomain.Chunk) ([]serverDomain.Hash, []int64) {
	hashes := make([]serverDomain.Hash, len(chunks))
	sizes := make([]int64, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		sizes[i] = c.SizeBytes
	}
	return hashes, sizes
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

// normalizeTarget cleans a repository-relative target; "" and "." both mean
// the whole working copy and come back as "".
func normalizeTarget(t string) (string, error) {
	if t == "" {
		return "", nil
	}
	if filepath.IsAbs(t) {
		return "", fmt.Errorf("path %q must be relative to the repository root", t)
	}
	clean := filepath.Clean(t)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the repository", t)
	}
	return filepath.ToSlash(clean), nil
}

// isNipaPath reports whether any segment of p names the metadata directory,
// including the case and Windows spellings that alias it on some filesystems.
func isNipaPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if serverDomain.IsMetadataSegment(seg) {
			return true
		}
	}
	return false
}
