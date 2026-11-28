package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/snow"
)

// rebaseCommitLimit caps how many source commits a rebase replays.
const rebaseCommitLimit = 1000

// mergeConflictLimit caps how many conflicted paths an error message names.
const mergeConflictLimit = 10

// MergeCommitOptions configures a non-fast-forward landing. Author is the
// commit author recorded for a merge commit and for a squash; rebase keeps the
// original commit authors.
type MergeCommitOptions struct {
	Strategy string
	Message  string
	Author   snow.ID
	// SourceCommitID pins the source head the gates were evaluated against.
	SourceCommitID *snow.ID
}

// mergeCommitter writes one or more prebuilt commits atomically.
type mergeCommitter interface {
	ApplyPushAll(ctx context.Context, reqs []ApplyPushRequest) error
}

// chunkUploader stores merge-generated content (through the same chunk
// storage service bundle uploads use, so it is packed like a bundle), then
// records its rows at the returned locations and attributes it to the
// project's storage ledger.
type chunkUploader interface {
	StoreChunks(ctx context.Context, projectID snow.ID, chunks []ChunkContent) ([]ChunkRow, error)
	RecordStoredChunks(ctx context.Context, projectID snow.ID, rows []ChunkRow) error
}

// chunkPrefetcher is implemented by chunk readers that can resolve the
// locations of many chunks at once before they are read one by one.
type chunkPrefetcher interface {
	Prefetch(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error
}

// WithMergeCommitter enables the non-fast-forward merge strategies.
func (b *Branch) WithMergeCommitter(committer mergeCommitter) *Branch {
	b.mergeCommitter = committer
	return b
}

// WithChunkUploader enables storing content produced by a text merge.
func (b *Branch) WithChunkUploader(uploads chunkUploader) *Branch {
	b.chunkUploads = uploads
	return b
}

// MergeForMergeRequest lands source onto target with the given strategy. The
// fast-forward strategy moves the target head; merge, squash and rebase author
// commits on the target with the three-way engine. A conflicted merge is
// refused with the conflicted paths and never writes anything.
func (b *Branch) MergeForMergeRequest(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string, opts MergeCommitOptions) (*domain.Branch, error) {
	if opts.Strategy == "" {
		opts.Strategy = domain.MergeStrategyFastForward
	}
	if !domain.IsValidMergeStrategy(opts.Strategy) {
		return nil, domain.NewErrorUser("strategy must be one of ff, merge, squash, rebase")
	}
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	if err := b.requireWritableStorage(ctx, projectID); err != nil {
		return nil, err
	}
	target, err := b.branchByName(ctx, projectID, targetBranch)
	if err != nil {
		return nil, err
	}
	source, err := b.branchByName(ctx, projectID, sourceBranch)
	if err != nil {
		return nil, err
	}
	if target.IsProtected && !b.permUc.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	if source.CommitID == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("cannot merge branch %q: source branch %q has no commits", targetBranch, sourceBranch))
	}
	if opts.SourceCommitID != nil && *source.CommitID != *opts.SourceCommitID {
		return nil, domain.NewErrorConflict(fmt.Sprintf("source branch %q changed since the merge request was checked; review it again", sourceBranch))
	}
	if target.CommitID == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("cannot merge into empty branch %q", targetBranch))
	}
	if *source.CommitID == *target.CommitID {
		return nil, domain.NewErrorConflict("source branch is already up to date with the target")
	}
	if opts.Strategy == domain.MergeStrategyFastForward {
		return b.fastForward(ctx, projectID, targetBranch, sourceBranch, true, opts.SourceCommitID)
	}
	if b.mergeCommitter == nil || b.chunkUploads == nil || b.chunks == nil {
		return nil, domain.NewErrorInternalServer("non-fast-forward merge strategies are not configured")
	}

	base, err := b.findMergeBase(ctx, projectID, target.CommitID, source.CommitID)
	if err != nil {
		return nil, err
	}
	targetHead, err := b.commitByIDInProject(ctx, projectID, *target.CommitID)
	if err != nil {
		return nil, err
	}

	if opts.Strategy == domain.MergeStrategyRebase && base != nil && *base == *target.CommitID {
		return b.fastForward(ctx, projectID, targetBranch, sourceBranch, true, opts.SourceCommitID)
	}

	var sourceHead *domain.Commit
	if opts.Strategy != domain.MergeStrategyRebase {
		if sourceHead, err = b.commitByIDInProject(ctx, projectID, *source.CommitID); err != nil {
			return nil, err
		}
	}
	var reqs []ApplyPushRequest
	parentID := target.CommitID
	parentHashes := []domain.Hash{targetHead.Hash}
	state, stored, err := b.replayMerge(ctx, projectID, opts.Strategy, base, *target.CommitID, *source.CommitID, func(step mergeLanding) error {
		root, err := buildNewTree(ctx, b.branchRepo, targetHead, step.files, step.removed)
		if err != nil {
			return mergeTreeClash(err, step.label)
		}
		if step.commit != nil {
			req := buildApplyRequest(b.snowNode, projectID, target.ID, parentID, nil, parentHashes, step.commit.Message, "", root)
			req.UserID = step.commit.UserID
			reqs = append(reqs, req)
			prev := req.CommitID
			parentID = &prev
			parentHashes = []domain.Hash{req.CommitHash}
			return nil
		}
		var parent2ID *snow.ID
		if opts.Strategy == domain.MergeStrategyMerge {
			parentHashes = append(parentHashes, sourceHead.Hash)
			id := sourceHead.ID
			parent2ID = &id
		}
		message := strings.TrimSpace(opts.Message)
		if message == "" {
			message = fmt.Sprintf("Merge branch '%s' into %s", sourceBranch, targetBranch)
		}
		req := buildApplyRequest(b.snowNode, projectID, target.ID, target.CommitID, parent2ID, parentHashes, message, "", root)
		req.UserID = opts.Author
		reqs = append(reqs, req)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if state == nil {
		// The source is already contained in the target: nothing to replay.
		return b.branchRepo.GetByProjectIDAndID(ctx, projectID, target.ID)
	}
	if err := b.ensureMergedWrites(ctx, projectID, state.target, state.current); err != nil {
		return nil, err
	}
	if err := b.chunkUploads.RecordStoredChunks(ctx, projectID, stored); err != nil {
		return nil, err
	}
	if err := b.mergeCommitter.ApplyPushAll(ctx, reqs); err != nil {
		return nil, err
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, target.ID)
}

// mergeLanding is one commit a merge lands, as a change against the target
// head: commit is the source commit a rebase replays (nil for merge and
// squash) and label prefixes its conflict errors.
type mergeLanding struct {
	files   []*domain.PushFile
	removed []string
	label   string
	commit  *domain.Commit
}

// replayMerge computes the commits a merge, squash or rebase lands on the
// target and hands each to land as soon as it is known, so a tree clash in
// one replayed commit is reported before a later commit is read. A nil state
// without an error means a rebase with nothing to replay.
func (b *Branch) replayMerge(ctx context.Context, projectID snow.ID, strategy string, base *snow.ID, targetID, sourceID snow.ID, land func(mergeLanding) error) (*mergeState, []ChunkRow, error) {
	state := newMergeState()
	if strategy != domain.MergeStrategyRebase {
		stored, err := b.mergeStep(ctx, projectID, state, base, targetID, sourceID, "")
		if err != nil {
			return nil, nil, err
		}
		files, removed := mergeDelta(state.target, state.current)
		if err := land(mergeLanding{files: files, removed: removed}); err != nil {
			return nil, nil, err
		}
		return state, stored, nil
	}
	commits, err := b.rebaseCommits(ctx, projectID, sourceID, base)
	if err != nil {
		return nil, nil, err
	}
	if len(commits) == 0 {
		return nil, nil, nil
	}
	var stored []ChunkRow
	for _, commit := range commits {
		label := fmt.Sprintf("commit %s", commit.Hash.String()[:10])
		added, err := b.mergeStep(ctx, projectID, state, commit.Parent1ID, targetID, commit.ID, label)
		if err != nil {
			return nil, nil, err
		}
		stored = append(stored, added...)
		files, removed := mergeDelta(state.target, state.current)
		if err := land(mergeLanding{files: files, removed: removed, label: label, commit: commit}); err != nil {
			return nil, nil, err
		}
	}
	return state, stored, nil
}

// mergeState is the target side of a merge, read only at the paths the
// replayed changes touch: target holds the target head's files at those
// paths and current the merge result so far. At every other path both would
// equal the target head, so mergeDelta and ensureMergedWrites over these maps
// give exactly what they give over the full trees.
type mergeState struct {
	target, current map[string]merge.File
	loaded          map[string]struct{}
}

func newMergeState() *mergeState {
	return &mergeState{target: map[string]merge.File{}, current: map[string]merge.File{}, loaded: map[string]struct{}{}}
}

// mergeStep runs one three-way merge of theirs onto the current state, with
// baseID (nil for the empty tree) as the common ancestor, and materializes
// text merges. merge.ThreeWay changes nothing at a path where theirs equals
// the base (it keeps ours, or deletes a file ours does not hold) and raises no
// conflict there, so it only needs the paths where base and theirs differ:
// those are read from the three trees, the target head's file at each one
// (unless an earlier step already read it) seeds the state. label prefixes a
// conflict error. On error nothing is written.
func (b *Branch) mergeStep(ctx context.Context, projectID snow.ID, state *mergeState, baseID *snow.ID, targetID, theirsID snow.ID, label string) ([]ChunkRow, error) {
	changed, err := b.mergeInputs(ctx, projectID, baseID, targetID, theirsID)
	if err != nil {
		return nil, err
	}
	base := make(map[string]merge.File, len(changed))
	ours := make(map[string]merge.File, len(changed))
	theirs := make(map[string]merge.File, len(changed))
	for _, file := range changed {
		path := file.path()
		if _, ok := state.loaded[path]; !ok {
			state.loaded[path] = struct{}{}
			if head := file.files[1]; head != nil {
				state.target[path] = merge.FromFile(path, head)
				state.current[path] = state.target[path]
			}
		}
		if current, ok := state.current[path]; ok {
			ours[path] = current
		}
		if f := file.files[0]; f != nil {
			base[path] = merge.FromFile(path, f)
		}
		if f := file.files[2]; f != nil {
			theirs[path] = merge.FromFile(path, f)
		}
	}
	result := merge.ThreeWay(base, ours, theirs)
	if len(result.Conflicts) > 0 {
		return nil, conflictError(conflictPaths(result), label)
	}
	var stored []ChunkRow
	state.current, stored, err = b.materialize(ctx, projectID, result, state.current)
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// mergeInputs compares the unfiltered trees of baseID (nil for the empty
// tree), targetID and theirsID where base and theirs differ: a merge must see
// every path to preserve it, so no read filter applies. Each entry holds the
// base, target and theirs file at one path, in that order.
func (b *Branch) mergeInputs(ctx context.Context, projectID snow.ID, baseID *snow.ID, targetID, theirsID snow.ID) ([]comparedFile, error) {
	var baseRoot *domain.TreeNode
	if baseID != nil {
		root, err := b.commitRoot(ctx, projectID, *baseID)
		if err != nil {
			return nil, err
		}
		baseRoot = root
	}
	theirsRoot, err := b.commitRoot(ctx, projectID, theirsID)
	if err != nil {
		return nil, err
	}
	targetRoot, err := b.commitRoot(ctx, projectID, targetID)
	if err != nil {
		return nil, err
	}
	return compareTrees(ctx, b.branchRepo, treeView{filter: AllowAllFilter()},
		[]*domain.TreeNode{baseRoot, targetRoot, theirsRoot}, []int{0, 2})
}

func (b *Branch) commitRoot(ctx context.Context, projectID snow.ID, commitID snow.ID) (*domain.TreeNode, error) {
	commit, err := b.commitByIDInProject(ctx, projectID, commitID)
	if err != nil {
		return nil, err
	}
	return b.branchRepo.GetTreeNode(ctx, commit.TreeID)
}

// materialize applies a three-way result to the current file set: taken files
// replace the current entry, text merges are computed and stored, deletions
// are dropped and conflicts are reported. It returns the chunks stored for
// text merges so the caller can attribute them. On error nothing is written.
func (b *Branch) materialize(ctx context.Context, projectID snow.ID, result *merge.Result, current map[string]merge.File) (map[string]merge.File, []ChunkRow, error) {
	merged := make(map[string]merge.File, len(current))
	for path, file := range current {
		merged[path] = file
	}
	var stored []ChunkRow
	for path, entry := range result.Entries {
		switch entry.Decision {
		case merge.KeepTheirs:
			merged[path] = entry.Theirs
		case merge.TextMerge:
			baseData, err := b.loadMergedContent(ctx, projectID, entry.Base)
			if err != nil {
				return nil, nil, err
			}
			oursData, err := b.loadMergedContent(ctx, projectID, entry.Ours)
			if err != nil {
				return nil, nil, err
			}
			theirsData, err := b.loadMergedContent(ctx, projectID, entry.Theirs)
			if err != nil {
				return nil, nil, err
			}
			content, conflicted := merge.MergeText(baseData, oursData, theirsData)
			if conflicted {
				return nil, nil, conflictError([]string{path}, "")
			}
			file, chunks, err := b.storeMergedContent(ctx, projectID, path, entry.Theirs.Mode, content)
			if err != nil {
				return nil, nil, err
			}
			stored = append(stored, chunks...)
			merged[path] = file
		}
	}
	for _, path := range result.Deleted {
		delete(merged, path)
	}
	return merged, stored, nil
}

func (b *Branch) loadMergedContent(ctx context.Context, projectID snow.ID, file merge.File) ([]byte, error) {
	if prefetcher, ok := b.chunks.(chunkPrefetcher); ok {
		if err := prefetcher.Prefetch(ctx, projectID, file.ChunkHashes); err != nil {
			return nil, err
		}
	}
	var out []byte
	for _, hash := range file.ChunkHashes {
		data, err := b.chunks.Get(ctx, projectID, hash)
		if err != nil {
			return nil, err
		}
		// Decode chunk by chunk: the decoder caps one chunk's output, and a
		// whole compressed file can legitimately be larger than that.
		if out, err = chunker.DecodeAppend(out, file.Encoding, data); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (b *Branch) storeMergedContent(ctx context.Context, projectID snow.ID, path string, mode int, content []byte) (merge.File, []ChunkRow, error) {
	encoding, chunks, err := chunker.EncodeBytes(content, path, false, "")
	if err != nil {
		return merge.File{}, nil, err
	}
	hashes := make([]domain.Hash, 0, len(chunks))
	sizes := make([]int64, 0, len(chunks))
	contents := make([]ChunkContent, 0, len(chunks))
	for _, chunk := range chunks {
		contents = append(contents, ChunkContent{Hash: chunk.Hash, Data: chunk.Data})
		hashes = append(hashes, chunk.Hash)
		sizes = append(sizes, chunk.SizeBytes)
	}
	stored, err := b.chunkUploads.StoreChunks(ctx, projectID, contents)
	if err != nil {
		return merge.File{}, nil, err
	}
	return merge.File{
		Path:        path,
		Mode:        mode,
		SizeBytes:   int64(len(content)),
		Encoding:    encoding,
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}, stored, nil
}

// rebaseCommits returns the source's first-parent commits that the base does
// not already contain, oldest first. Stopping on containment (rather than
// equality) keeps the walk correct when the merge base lives on a second-parent
// chain, e.g. after the source merged the target.
func (b *Branch) rebaseCommits(ctx context.Context, projectID snow.ID, head snow.ID, base *snow.ID) ([]*domain.Commit, error) {
	contained, err := b.reachableFrom(ctx, projectID, base)
	if err != nil {
		return nil, err
	}
	var out []*domain.Commit
	seen := map[snow.ID]bool{}
	current := head
	for !contained[current] {
		if seen[current] {
			return nil, domain.NewErrorInternalServer("commit graph cycle")
		}
		seen[current] = true
		commit, err := b.commitByIDInProject(ctx, projectID, current)
		if err != nil {
			return nil, err
		}
		out = append(out, commit)
		if len(out) > rebaseCommitLimit {
			return nil, domain.NewErrorUser(fmt.Sprintf("cannot rebase more than %d commits", rebaseCommitLimit))
		}
		if commit.Parent1ID == nil {
			break
		}
		current = *commit.Parent1ID
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// reachableFrom collects every commit reachable from start over both parents,
// start included.
func (b *Branch) reachableFrom(ctx context.Context, projectID snow.ID, start *snow.ID) (map[snow.ID]bool, error) {
	reached := map[snow.ID]bool{}
	if start == nil {
		return reached, nil
	}
	stack := []snow.ID{*start}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reached[id] {
			continue
		}
		reached[id] = true
		commit, err := b.commitByIDInProject(ctx, projectID, id)
		if err != nil {
			return nil, err
		}
		if commit.Parent1ID != nil {
			stack = append(stack, *commit.Parent1ID)
		}
		if commit.Parent2ID != nil {
			stack = append(stack, *commit.Parent2ID)
		}
	}
	return reached, nil
}

// mergeDelta expresses a merged file set as a delta against the target head
// tree: changed and added files plus removed paths.
func mergeDelta(target, merged map[string]merge.File) ([]*domain.PushFile, []string) {
	var files []*domain.PushFile
	for path, file := range merged {
		if old, ok := target[path]; ok && old.Hash == file.Hash && old.Mode == file.Mode {
			continue
		}
		files = append(files, &domain.PushFile{
			Path:        path,
			Mode:        file.Mode,
			SizeBytes:   file.SizeBytes,
			IsBinary:    file.IsBinary,
			Encoding:    file.Encoding,
			FileHash:    file.Hash,
			ChunkHashes: file.ChunkHashes,
		})
	}
	var removed []string
	for path := range target {
		if _, ok := merged[path]; !ok {
			removed = append(removed, path)
		}
	}
	sort.Strings(removed)
	return files, removed
}

// ensureMergedWrites rejects a merge that changes paths the caller may not
// write, regardless of read permission.
func (b *Branch) ensureMergedWrites(ctx context.Context, projectID snow.ID, target, merged map[string]merge.File) error {
	check := func(path string) error {
		if !b.permUc.HasPathAccess(ctx, projectID, path, domain.PermissionWrite) {
			return domain.NewErrorNoPermission()
		}
		return nil
	}
	for path, file := range merged {
		if old, ok := target[path]; ok && old.Hash == file.Hash && old.Mode == file.Mode {
			continue
		}
		if err := check(path); err != nil {
			return err
		}
	}
	for path := range target {
		if _, ok := merged[path]; !ok {
			if err := check(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func conflictPaths(result *merge.Result) []string {
	paths := make([]string, 0, len(result.Conflicts))
	for _, conflict := range result.Conflicts {
		paths = append(paths, conflict.Path)
	}
	return paths
}

func conflictError(paths []string, prefix string) error {
	sort.Strings(paths)
	shown := paths
	suffix := ""
	if len(paths) > mergeConflictLimit {
		shown = paths[:mergeConflictLimit]
		suffix = fmt.Sprintf(" (+%d more)", len(paths)-mergeConflictLimit)
	}
	if prefix != "" {
		prefix += ": "
	}
	return domain.NewErrorConflict(fmt.Sprintf("%smerge conflicts in %s%s", prefix, strings.Join(shown, ", "), suffix))
}
