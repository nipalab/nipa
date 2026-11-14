package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
)

type ShelveRepo interface {
	WorkingCopyRepo
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
	CreateShelf(build func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error)) (*domain.Shelf, error)
	ListShelves() ([]domain.Shelf, error)
	LoadShelf(id string) (*domain.Shelf, error)
	DropShelf(id string) error
	OpenShelfChunk(id string, hash serverDomain.Hash) (io.ReadCloser, error)
	LoadShelfChunk(id string, hash serverDomain.Hash) ([]byte, error)
	HasShelfChunk(id string, hash serverDomain.Hash) bool
	LoadShelfState() (*domain.ShelfState, error)
	SaveShelfState(state *domain.ShelfState) error
	ClearShelfState() error
}

// Shelve parks local changes in .nipa/shelves and brings them back later
// through the three-way core. Shelves are local only.
type Shelve struct {
	repo ShelveRepo
	wc   *WorkingCopy
	root string
	now  func() time.Time
}

func NewShelve(repo ShelveRepo, wc *WorkingCopy, root string) *Shelve {
	return &Shelve{repo: repo, wc: wc, root: root, now: time.Now}
}

type ShelveOptions struct {
	Name string
	// Paths narrows the shelf to these files or folders; empty takes every
	// change. Untracked files are taken when named exactly or when
	// IncludeUntracked is set.
	Paths            []string
	IncludeUntracked bool
}

type ShelfApplyResult struct {
	Shelf     *domain.Shelf
	Conflicts []string
	Dropped   bool
}

type shelfCandidate struct {
	status    string
	staged    bool
	untracked bool
}

func (s *Shelve) List() ([]domain.Shelf, error) {
	return s.repo.ListShelves()
}

func (s *Shelve) Drop(id string) error {
	state, err := s.repo.LoadShelfState()
	if err != nil {
		return err
	}
	if state != nil && state.ShelfID == id {
		return domain.NewUserError("this shelf is being applied; resolve the conflicts or abort the apply first")
	}
	return s.repo.DropShelf(id)
}

func (s *Shelve) refusePending() error {
	if err := refuseMergeOrRevert(s.repo); err != nil {
		return err
	}
	return refuseShelfPending(s.repo)
}

func refuseMergeOrRevert(repo interface {
	LoadMergeState() (*domain.MergeState, error)
	LoadRevertState() (*domain.RevertState, error)
}) error {
	m, err := repo.LoadMergeState()
	if err != nil {
		return err
	}
	if m != nil {
		return domain.NewUserError("a merge is in progress; finish or abort it first")
	}
	r, err := repo.LoadRevertState()
	if err != nil {
		return err
	}
	if r != nil {
		return domain.NewUserError("a revert is in progress; continue or abort it first")
	}
	return nil
}

// refuseShelfPending fails while a shelf apply waits on conflicts. Repos
// without shelf support never have one.
func refuseShelfPending(repo any) error {
	l, ok := repo.(shelfStateLoader)
	if !ok {
		return nil
	}
	state, err := l.LoadShelfState()
	if err != nil {
		return err
	}
	if state != nil {
		return domain.NewUserError(fmt.Sprintf("applying shelf %q stopped on conflicts; resolve them or abort the apply first", state.ShelfName)).
			WithHint("resolve the conflicts, or put the files back as they were", "nipa shelve apply --abort")
	}
	return nil
}

// Create writes the selected changes into a new shelf and only then puts
// those paths back to the synced version: tracked files are restored, new
// files removed and staged entries unstaged.
func (s *Shelve) Create(ctx context.Context, opts ShelveOptions) (*domain.Shelf, error) {
	unlock, err := lockShared(s.repo)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.refusePending(); err != nil {
		return nil, err
	}
	st, err := s.wc.Status(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.repo.Snapshot()
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		tracked[f.Path] = f
	}
	candidates := shelfCandidates(st, tracked)
	selected, err := selectShelfPaths(candidates, opts)
	if err != nil {
		return nil, err
	}
	cfg, err := s.repo.LoadConfig()
	if err != nil {
		return nil, err
	}
	commit, err := s.repo.LoadCommit()
	if err != nil {
		return nil, err
	}
	pins, err := loadPins(s.repo)
	if err != nil {
		return nil, err
	}

	var restore []string
	var restoreFiles []domain.SnapshotFile
	for _, p := range selected {
		if base, ok := tracked[p]; ok {
			restore = append(restore, p)
			restoreFiles = append(restoreFiles, base)
		}
	}
	if err := s.wc.pristine().ensureSnapshotContent(ctx, restoreFiles); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultShelfName(len(selected), cfg)
	}
	shelf, err := s.repo.CreateShelf(func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		out := &domain.Shelf{
			Name:         name,
			CreatedAt:    s.now().UTC(),
			Branch:       cfg.Branch,
			Head:         cfg.Head,
			BaseTreeHash: snapshot.TreeHash,
		}
		if commit != nil {
			out.BaseCommitID = commit.CommitID
		}
		for _, p := range selected {
			entry, err := s.shelveEntry(p, candidates[p], tracked, pins, put)
			if err != nil {
				return nil, err
			}
			out.Entries = append(out.Entries, entry)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.clearShelved(ctx, shelf, restore, tracked); err != nil {
		return shelf, fmt.Errorf("shelf %q was saved, but the working copy was not fully put back: %w", shelf.Name, err)
	}
	return shelf, nil
}

func defaultShelfName(n int, cfg *domain.Config) string {
	files := "files"
	if n == 1 {
		files = "file"
	}
	where := cfg.Branch
	if cfg.Head != nil {
		where = cfg.Head.Name
	}
	return fmt.Sprintf("%d %s on %s", n, files, where)
}

func shelfCandidates(st *domain.Status, tracked map[string]domain.SnapshotFile) map[string]shelfCandidate {
	out := map[string]shelfCandidate{}
	added := toSet(st.Added)
	for _, p := range st.Staged {
		if added[p] {
			out[p] = shelfCandidate{status: domain.ShelfAdded, staged: true}
		} else {
			out[p] = shelfCandidate{status: domain.ShelfModified, staged: true}
		}
	}
	for _, p := range st.Deleted {
		if _, ok := tracked[p]; ok {
			out[p] = shelfCandidate{status: domain.ShelfDeleted, staged: true}
		}
	}
	for _, p := range st.Modified {
		out[p] = shelfCandidate{status: domain.ShelfModified}
	}
	for _, p := range st.Missing {
		out[p] = shelfCandidate{status: domain.ShelfDeleted}
	}
	for _, p := range st.Untracked {
		out[p] = shelfCandidate{status: domain.ShelfAdded, untracked: true}
	}
	return out
}

func toSet(paths []string) map[string]bool {
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out
}

func selectShelfPaths(candidates map[string]shelfCandidate, opts ShelveOptions) ([]string, error) {
	seen := map[string]bool{}
	if len(opts.Paths) == 0 {
		for p, c := range candidates {
			if !c.untracked || opts.IncludeUntracked {
				seen[p] = true
			}
		}
	}
	for _, raw := range opts.Paths {
		target, err := normalizeTarget(raw)
		if err != nil {
			return nil, err
		}
		if isNipaPath(target) {
			return nil, fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		matched := false
		for p, c := range candidates {
			exact := p == target
			if !exact && target != "" && !strings.HasPrefix(p, target+"/") {
				continue
			}
			if c.untracked && !exact && !opts.IncludeUntracked {
				continue
			}
			seen[p] = true
			matched = true
		}
		if !matched {
			return nil, domain.NewUserError(fmt.Sprintf("%s has no changes to shelve", raw))
		}
	}
	if len(seen) == 0 {
		return nil, domain.NewUserError("there are no changes to shelve")
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Shelve) shelveEntry(p string, c shelfCandidate, tracked map[string]domain.SnapshotFile, pins []string, put func(serverDomain.Hash, []byte) error) (domain.ShelfEntry, error) {
	entry := domain.ShelfEntry{Path: p, Status: c.status, Staged: c.staged, Untracked: c.untracked, Locked: pinsCover(pins, p)}
	base, isTracked := tracked[p]
	if isTracked {
		entry.Base = shelfFileOf(base)
		if !base.IsBinary && base.Encoding == chunker.EncodingZstd {
			entry.BaseStored = s.copyChunks(base.Chunks, put) == nil
		}
	}
	if c.status == domain.ShelfDeleted {
		return entry, nil
	}
	file, err := s.shelveWorkingFile(p, base, isTracked, put)
	if err != nil {
		return entry, err
	}
	entry.File = file
	return entry, nil
}

func (s *Shelve) copyChunks(hashes []serverDomain.Hash, put func(serverDomain.Hash, []byte) error) error {
	for _, h := range hashes {
		data, err := s.repo.LoadChunk(h)
		if err != nil {
			return err
		}
		if err := put(h, data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shelve) shelveWorkingFile(p string, base domain.SnapshotFile, isTracked bool, put func(serverDomain.Hash, []byte) error) (*domain.ShelfFile, error) {
	f, err := os.Open(filepath.Join(s.root, filepath.FromSlash(p)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, domain.NewUserError(fmt.Sprintf("%s is not a regular file", p))
	}
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return nil, err
	}
	encoding := ""
	mode := serverModeFromPerm(info.Mode())
	if isTracked {
		encoding = storedEncoding(base.Encoding)
		mode = trackedModeFromPerm(info.Mode(), base.Mode)
	}
	var hashes []serverDomain.Hash
	encoding, err = chunker.Encode(f, p, isBinary, encoding, func(c chunker.EncodedChunk) error {
		hashes = append(hashes, c.Hash)
		return put(c.Hash, c.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("shelve %s: %w", p, err)
	}
	return &domain.ShelfFile{
		Hash:      chunker.FileHash(hashes).String(),
		Mode:      mode,
		SizeBytes: info.Size(),
		IsBinary:  isBinary,
		Encoding:  encoding,
		Chunks:    hexHashes(hashes),
	}, nil
}

func (s *Shelve) clearShelved(ctx context.Context, shelf *domain.Shelf, restore []string, tracked map[string]domain.SnapshotFile) error {
	var staged, added []string
	for _, e := range shelf.Entries {
		if e.Staged {
			staged = append(staged, e.Path)
		}
		if _, ok := tracked[e.Path]; !ok {
			added = append(added, e.Path)
		}
	}
	if len(staged) > 0 {
		if err := s.repo.StageRemove(staged); err != nil {
			return err
		}
	}
	if len(restore) > 0 {
		if err := s.wc.restoreSnapshot(ctx, restore, tracked); err != nil {
			return err
		}
	}
	for _, p := range added {
		if err := removeWorkingFile(s.root, p); err != nil {
			return err
		}
	}
	return nil
}

// removeWorkingFile deletes a file and the folders it leaves empty.
func removeWorkingFile(root, p string) error {
	abs := filepath.Join(root, filepath.FromSlash(p))
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	cleanRoot := filepath.Clean(root)
	for dir := filepath.Dir(abs); dir != cleanRoot && strings.HasPrefix(dir, cleanRoot); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// Apply brings a shelf back onto the working copy: base is the version the
// files were shelved from, ours the synced snapshot, theirs the shelf. Paths
// with local changes are refused. Conflicts leave the apply waiting in
// shelf_state with the shelf kept; a clean apply drops it unless keep.
func (s *Shelve) Apply(ctx context.Context, id string, keep bool) (*ShelfApplyResult, error) {
	unlock, err := lockShared(s.repo)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.refusePending(); err != nil {
		return nil, err
	}
	shelf, err := s.repo.LoadShelf(id)
	if err != nil {
		return nil, err
	}
	cfg, err := s.repo.LoadConfig()
	if err != nil {
		return nil, err
	}
	if err := s.refuseDirty(ctx, shelf, cfg); err != nil {
		return nil, err
	}
	snapshot, err := s.repo.Snapshot()
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		tracked[f.Path] = f
	}

	base, ours, theirs := map[string]merge.File{}, map[string]merge.File{}, map[string]merge.File{}
	oursByPath := map[string]domain.SnapshotFile{}
	var oursFiles []domain.SnapshotFile
	for _, e := range shelf.Entries {
		if e.Base != nil {
			f, err := mergeFileOf(e.Path, e.Base)
			if err != nil {
				return nil, err
			}
			base[e.Path] = f
		}
		if e.File != nil {
			f, err := mergeFileOf(e.Path, e.File)
			if err != nil {
				return nil, err
			}
			theirs[e.Path] = f
		}
		if cur, ok := tracked[e.Path]; ok {
			ours[e.Path] = snapshotMergeFile(cur)
			oursByPath[e.Path] = cur
			oursFiles = append(oursFiles, cur)
		}
	}
	if err := s.wc.pristine().ensureSnapshotContent(ctx, oursFiles); err != nil {
		return nil, err
	}

	res := merge.ThreeWay(base, ours, theirs)
	overlay := shelfOverlay{ShelveRepo: s.repo, id: shelf.ID}
	applied, err := applyThreeWay(ctx, noDownload{}, overlay, s.root, ours, oursByPath, res, domain.ChunkScope{}, true)
	if err != nil {
		return nil, err
	}

	conflicted := toSet(applied.Conflicted)
	var unstage []string
	for _, p := range applied.Staged {
		e, _ := shelf.Entry(p)
		if !e.Staged || conflicted[p] {
			unstage = append(unstage, p)
		}
	}
	if len(unstage) > 0 {
		if err := s.repo.StageRemove(unstage); err != nil {
			return nil, err
		}
	}

	out := &ShelfApplyResult{Shelf: shelf, Conflicts: applied.Conflicted}
	if len(applied.Conflicted) > 0 {
		state := &domain.ShelfState{
			ShelfID:        shelf.ID,
			ShelfName:      shelf.Name,
			BaseCommitID:   shelf.BaseCommitID,
			Keep:           keep,
			Applied:        mergedPaths(applied.Staged, applied.Conflicted),
			Conflicts:      applied.Conflicted,
			ConflictKinds:  applied.Kinds,
			ConflictBinary: applied.Binary,
		}
		if commit, err := s.repo.LoadCommit(); err == nil && commit != nil {
			state.OursCommitID = commit.CommitID
		}
		if err := s.repo.SaveShelfState(state); err != nil {
			return nil, err
		}
		return out, nil
	}
	if !keep {
		if err := s.repo.DropShelf(shelf.ID); err != nil {
			return nil, err
		}
		out.Dropped = true
	}
	return out, nil
}

func mergedPaths(a, b []string) []string {
	set := toSet(a)
	for _, p := range b {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (s *Shelve) refuseDirty(ctx context.Context, shelf *domain.Shelf, cfg *domain.Config) error {
	var outside []string
	for _, e := range shelf.Entries {
		if !sparseCovers(cfg.Sparse, e.Path) {
			outside = append(outside, e.Path)
		}
	}
	if len(outside) > 0 {
		return domain.NewUserError(fmt.Sprintf("%s is outside this clone's checkout; add its folder to the checkout first", strings.Join(outside, ", ")))
	}
	st, err := s.wc.Status(ctx)
	if err != nil {
		return err
	}
	dirty := toSet(st.Staged)
	for _, list := range [][]string{st.Deleted, st.Modified, st.Missing, st.Untracked} {
		for _, p := range list {
			dirty[p] = true
		}
	}
	var hits []string
	for _, e := range shelf.Entries {
		if dirty[e.Path] {
			hits = append(hits, e.Path)
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return domain.NewUserError(fmt.Sprintf("local changes to %s would be overwritten; shelve or discard them first", strings.Join(hits, ", ")))
}

// Abort stops a shelf apply waiting on conflicts: every path it wrote goes
// back to the synced version (new files are removed) and the shelf is kept.
func (s *Shelve) Abort(ctx context.Context) error {
	unlock, err := lockShared(s.repo)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := s.repo.LoadShelfState()
	if err != nil {
		return err
	}
	if state == nil {
		return domain.NewUserError("no shelf apply is waiting")
	}
	snapshot, err := s.repo.Snapshot()
	if err != nil {
		return err
	}
	tracked := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		tracked[f.Path] = f
	}
	paths := mergedPaths(state.Applied, state.Conflicts)
	if err := s.repo.StageRemove(paths); err != nil {
		return err
	}
	var restore []string
	for _, p := range paths {
		if _, ok := tracked[p]; ok {
			restore = append(restore, p)
			continue
		}
		if err := removeWorkingFile(s.root, p); err != nil {
			return err
		}
	}
	if err := s.wc.restoreSnapshot(ctx, restore, tracked); err != nil {
		return err
	}
	return s.repo.ClearShelfState()
}

// Continue finishes a shelf apply once the conflicted files were edited:
// text files must be free of conflict markers. The files stay unstaged and
// the shelf is dropped unless the apply kept it.
func (s *Shelve) Continue() (*domain.ShelfState, error) {
	unlock, err := lockShared(s.repo)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := s.repo.LoadShelfState()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, domain.NewUserError("no shelf apply is waiting")
	}
	binary := toSet(state.ConflictBinary)
	for _, p := range state.Conflicts {
		if binary[p] {
			continue
		}
		if err := checkWorkingMarkers(filepath.Join(s.root, filepath.FromSlash(p)), p); err != nil {
			return nil, err
		}
	}
	if err := s.repo.ClearShelfState(); err != nil {
		return nil, err
	}
	if !state.Keep {
		if err := dropFinishedShelf(s.repo, state.ShelfID); err != nil {
			return nil, err
		}
	}
	return state, nil
}

// Show returns a shelf's changes against the version they were shelved
// from, with text content attached for hunks.
func (s *Shelve) Show(id string) (*domain.Shelf, []diff.FileDiff, error) {
	shelf, err := s.repo.LoadShelf(id)
	if err != nil {
		return nil, nil, err
	}
	out := make([]diff.FileDiff, 0, len(shelf.Entries))
	for _, e := range shelf.Entries {
		fd := diff.FileDiff{Change: diff.Change{Path: e.Path}}
		switch {
		case e.File == nil:
			fd.Change.Status = diff.Deleted
		case e.Base == nil:
			fd.Change.Status = diff.Added
		default:
			fd.Change.Status = diff.Modified
		}
		if e.Base != nil {
			fd.Change.Old = diffEntryOf(e.Path, e.Base)
			if !e.Base.IsBinary {
				if e.BaseStored {
					fd.Old, err = s.shelfContent(id, fd.Change.Old)
				} else {
					fd.Old, err = loadEntryContent(s.repo.LoadChunk, fd.Change.Old)
				}
				fd.OldUnavailable = err != nil
			}
		}
		if e.File != nil {
			fd.Change.New = diffEntryOf(e.Path, e.File)
			if !e.File.IsBinary {
				fd.New, err = s.shelfContent(id, fd.Change.New)
				fd.NewUnavailable = err != nil
			}
		}
		out = append(out, fd)
	}
	return shelf, out, nil
}

func (s *Shelve) shelfContent(id string, e diff.Entry) ([]byte, error) {
	return loadEntryContent(func(h serverDomain.Hash) ([]byte, error) { return s.repo.LoadShelfChunk(id, h) }, e)
}

func loadEntryContent(load func(serverDomain.Hash) ([]byte, error), e diff.Entry) ([]byte, error) {
	return io.ReadAll(&blobReader{load: load, entry: e})
}

// ShelfEntryBlob resolves one side of a shelved path: the shelved content,
// or with base the version it was shelved from. A base not stored in the
// shelf reports ok false so the caller can read it from the base commit.
func ShelfEntryBlob(repo ShelfReader, id, p string, base bool) (diff.Entry, func(serverDomain.Hash) ([]byte, error), bool, error) {
	shelf, err := repo.LoadShelf(id)
	if err != nil {
		return diff.Entry{}, nil, false, err
	}
	e, found := shelf.Entry(p)
	file := e.File
	if base {
		file = e.Base
	}
	if !found || file == nil {
		return diff.Entry{}, nil, false, missingFile(fmt.Sprintf("%s is not in this side of shelf %s", p, id))
	}
	entry := diffEntryOf(p, file)
	if base && !e.BaseStored {
		return entry, nil, false, nil
	}
	load := func(h serverDomain.Hash) ([]byte, error) { return repo.LoadShelfChunk(id, h) }
	return entry, load, true, nil
}

type ShelfReader interface {
	LoadShelf(id string) (*domain.Shelf, error)
	LoadShelfChunk(id string, hash serverDomain.Hash) ([]byte, error)
}

func shelfFileOf(f domain.SnapshotFile) *domain.ShelfFile {
	return &domain.ShelfFile{
		Hash:      f.Hash.String(),
		Mode:      f.Mode,
		SizeBytes: f.SizeBytes,
		IsBinary:  f.IsBinary,
		Encoding:  storedEncoding(f.Encoding),
		Chunks:    hexHashes(f.Chunks),
	}
}

func hexHashes(hashes []serverDomain.Hash) []string {
	out := make([]string, len(hashes))
	for i, h := range hashes {
		out[i] = h.String()
	}
	return out
}

func parseHashes(hexes []string) ([]serverDomain.Hash, error) {
	out := make([]serverDomain.Hash, len(hexes))
	for i, h := range hexes {
		parsed, err := serverDomain.ParseHashHex(h)
		if err != nil {
			return nil, fmt.Errorf("shelf chunk %q: %w", h, err)
		}
		out[i] = parsed
	}
	return out, nil
}

func mergeFileOf(p string, f *domain.ShelfFile) (merge.File, error) {
	hashes, err := parseHashes(f.Chunks)
	if err != nil {
		return merge.File{}, err
	}
	return merge.File{
		Path:        p,
		Mode:        normalizedMode(f.Mode),
		SizeBytes:   f.SizeBytes,
		IsBinary:    f.IsBinary,
		Encoding:    storedEncoding(f.Encoding),
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}, nil
}

func snapshotMergeFile(f domain.SnapshotFile) merge.File {
	return merge.File{
		Path:        f.Path,
		Mode:        normalizedMode(f.Mode),
		SizeBytes:   f.SizeBytes,
		IsBinary:    f.IsBinary,
		Encoding:    storedEncoding(f.Encoding),
		Hash:        f.Hash,
		ChunkHashes: f.Chunks,
	}
}

func diffEntryOf(p string, f *domain.ShelfFile) diff.Entry {
	hashes, _ := parseHashes(f.Chunks)
	return diff.Entry{
		Path:        p,
		Mode:        f.Mode,
		SizeBytes:   f.SizeBytes,
		IsBinary:    f.IsBinary,
		Encoding:    storedEncoding(f.Encoding),
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}
}

// shelfOverlay reads chunks from the shelf before the object cache, so the
// three-way core can materialize shelved content without copying it.
type shelfOverlay struct {
	ShelveRepo
	id string
}

func (o shelfOverlay) OpenChunk(h serverDomain.Hash) (io.ReadCloser, error) {
	if o.HasShelfChunk(o.id, h) {
		return o.OpenShelfChunk(o.id, h)
	}
	return o.ShelveRepo.OpenChunk(h)
}

func (o shelfOverlay) LoadChunk(h serverDomain.Hash) ([]byte, error) {
	if o.HasShelfChunk(o.id, h) {
		return o.LoadShelfChunk(o.id, h)
	}
	return o.ShelveRepo.LoadChunk(h)
}

func (o shelfOverlay) MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error) {
	var rest []serverDomain.Hash
	for _, h := range hashes {
		if !o.HasShelfChunk(o.id, h) {
			rest = append(rest, h)
		}
	}
	if len(rest) == 0 {
		return nil, nil
	}
	return o.ShelveRepo.MissingChunks(rest)
}

type noDownload struct{}

func (noDownload) DownloadChunks(context.Context, domain.ChunkScope, []serverDomain.Hash, func(serverDomain.Hash, []byte) error) error {
	return domain.NewUserError("the version this shelf was made from is not cached locally; apply it on a clone that has it")
}
