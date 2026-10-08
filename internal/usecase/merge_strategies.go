package usecase

import (
	"bytes"
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
}

// mergeCommitter writes one or more prebuilt commits atomically.
type mergeCommitter interface {
	ApplyPushAll(ctx context.Context, reqs []ApplyPushRequest) error
}

// chunkUploader stores merge-generated content and records its metadata.
type chunkUploader interface {
	Upload(ctx context.Context, hash domain.Hash, data []byte) (bool, error)
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
	if target.CommitID == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("cannot merge into empty branch %q", targetBranch))
	}
	if *source.CommitID == *target.CommitID {
		return nil, domain.NewErrorConflict("source branch is already up to date with the target")
	}
	if opts.Strategy == domain.MergeStrategyFastForward {
		return b.FastForwardForMergeRequest(ctx, projectID, targetBranch, sourceBranch)
	}
	if b.mergeCommitter == nil || b.chunkUploads == nil || b.chunks == nil {
		return nil, domain.NewErrorInternalServer("non-fast-forward merge strategies are not configured")
	}

	base, err := b.findMergeBase(ctx, target.CommitID, source.CommitID)
	if err != nil {
		return nil, err
	}
	targetHead, err := b.commitByIDInProject(ctx, projectID, *target.CommitID)
	if err != nil {
		return nil, err
	}
	targetTree, err := b.mergeTree(ctx, projectID, *target.CommitID)
	if err != nil {
		return nil, err
	}

	if opts.Strategy == domain.MergeStrategyRebase && base != nil && *base == *target.CommitID {
		return b.FastForwardForMergeRequest(ctx, projectID, targetBranch, sourceBranch)
	}

	var reqs []ApplyPushRequest
	var merged map[string]merge.File
	switch opts.Strategy {
	case domain.MergeStrategyMerge, domain.MergeStrategySquash:
		sourceHead, err := b.commitByIDInProject(ctx, projectID, *source.CommitID)
		if err != nil {
			return nil, err
		}
		merged, err = b.mergeOnto(ctx, projectID, base, *target.CommitID, *source.CommitID, targetTree)
		if err != nil {
			return nil, err
		}
		files, removed := mergeDelta(targetTree, merged)
		root, err := buildNewTree(ctx, b.branchRepo, targetHead, files, removed)
		if err != nil {
			return nil, err
		}
		parentHashes := []domain.Hash{targetHead.Hash}
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
		req := buildApplyRequest(b.snowNode, projectID, target.ID, target.CommitID, parent2ID, parentHashes, message, root)
		req.UserID = opts.Author
		reqs = append(reqs, req)
	case domain.MergeStrategyRebase:
		commits, err := b.rebaseCommits(ctx, projectID, *source.CommitID, base)
		if err != nil {
			return nil, err
		}
		parentID := target.CommitID
		parentHashes := []domain.Hash{targetHead.Hash}
		current := targetTree
		for _, commit := range commits {
			var commitBase map[string]merge.File
			if commit.Parent1ID != nil {
				commitBase, err = b.mergeTree(ctx, projectID, *commit.Parent1ID)
				if err != nil {
					return nil, err
				}
			} else {
				commitBase = map[string]merge.File{}
			}
			commitTree, err := b.mergeTree(ctx, projectID, commit.ID)
			if err != nil {
				return nil, err
			}
			result := merge.ThreeWay(commitBase, current, commitTree)
			if len(result.Conflicts) > 0 {
				return nil, conflictError(conflictPaths(result), fmt.Sprintf("commit %s", commit.Hash.String()[:10]))
			}
			current, err = b.materialize(ctx, result, current)
			if err != nil {
				return nil, err
			}
			files, removed := mergeDelta(targetTree, current)
			root, err := buildNewTree(ctx, b.branchRepo, targetHead, files, removed)
			if err != nil {
				return nil, err
			}
			req := buildApplyRequest(b.snowNode, projectID, target.ID, parentID, nil, parentHashes, commit.Message, root)
			req.UserID = commit.UserID
			reqs = append(reqs, req)
			prev := req.CommitID
			parentID = &prev
			parentHashes = []domain.Hash{req.CommitHash}
			merged = current
		}
	}

	if merged == nil {
		return nil, domain.NewErrorInternalServer("merge produced no result")
	}
	if err := b.ensureMergedWrites(ctx, projectID, targetTree, merged); err != nil {
		return nil, err
	}
	if err := b.mergeCommitter.ApplyPushAll(ctx, reqs); err != nil {
		return nil, err
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, target.ID)
}

// mergeOnto runs one three-way merge and materializes text merges. The result
// is the complete post-merge file set.
func (b *Branch) mergeOnto(ctx context.Context, projectID snow.ID, baseID *snow.ID, oursID, theirsID snow.ID, ours map[string]merge.File) (map[string]merge.File, error) {
	var base map[string]merge.File
	if baseID != nil {
		var err error
		base, err = b.mergeTree(ctx, projectID, *baseID)
		if err != nil {
			return nil, err
		}
	} else {
		base = map[string]merge.File{}
	}
	theirs, err := b.mergeTree(ctx, projectID, theirsID)
	if err != nil {
		return nil, err
	}
	result := merge.ThreeWay(base, ours, theirs)
	if len(result.Conflicts) > 0 {
		return nil, conflictError(conflictPaths(result), "")
	}
	return b.materialize(ctx, result, ours)
}

// materialize applies a three-way result to the current file set: taken files
// replace the current entry, text merges are computed and stored, deletions
// are dropped and conflicts are reported. On error nothing is written.
func (b *Branch) materialize(ctx context.Context, result *merge.Result, current map[string]merge.File) (map[string]merge.File, error) {
	merged := make(map[string]merge.File, len(current))
	for path, file := range current {
		merged[path] = file
	}
	for path, entry := range result.Entries {
		switch entry.Decision {
		case merge.KeepTheirs:
			merged[path] = entry.Theirs
		case merge.TextMerge:
			baseData, err := b.loadMergedContent(ctx, entry.Base)
			if err != nil {
				return nil, err
			}
			oursData, err := b.loadMergedContent(ctx, entry.Ours)
			if err != nil {
				return nil, err
			}
			theirsData, err := b.loadMergedContent(ctx, entry.Theirs)
			if err != nil {
				return nil, err
			}
			content, conflicted := merge.MergeText(baseData, oursData, theirsData)
			if conflicted {
				return nil, conflictError([]string{path}, "")
			}
			file, err := b.storeMergedContent(ctx, path, entry.Theirs.Mode, content)
			if err != nil {
				return nil, err
			}
			merged[path] = file
		}
	}
	for _, path := range result.Deleted {
		delete(merged, path)
	}
	return merged, nil
}

func (b *Branch) loadMergedContent(ctx context.Context, file merge.File) ([]byte, error) {
	parts := make([][]byte, 0, len(file.ChunkHashes))
	for _, hash := range file.ChunkHashes {
		data, err := b.chunks.Get(ctx, hash)
		if err != nil {
			return nil, err
		}
		parts = append(parts, data)
	}
	return chunker.Decode(file.Encoding, bytes.Join(parts, nil))
}

func (b *Branch) storeMergedContent(ctx context.Context, path string, mode int, content []byte) (merge.File, error) {
	encoding, chunks, err := chunker.EncodeBytes(content, path, false, "")
	if err != nil {
		return merge.File{}, err
	}
	hashes := make([]domain.Hash, 0, len(chunks))
	sizes := make([]int64, 0, len(chunks))
	for _, chunk := range chunks {
		if _, err := b.chunkUploads.Upload(ctx, chunk.Hash, chunk.Data); err != nil {
			return merge.File{}, err
		}
		hashes = append(hashes, chunk.Hash)
		sizes = append(sizes, chunk.SizeBytes)
	}
	return merge.File{
		Path:        path,
		Mode:        mode,
		SizeBytes:   int64(len(content)),
		Encoding:    encoding,
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}, nil
}

// mergeTree loads a commit's complete tree, unfiltered: a merge must see every
// path to preserve it.
func (b *Branch) mergeTree(ctx context.Context, projectID snow.ID, commitID snow.ID) (map[string]merge.File, error) {
	commit, err := b.commitByIDInProject(ctx, projectID, commitID)
	if err != nil {
		return nil, err
	}
	root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, err
	}
	if err := b.loadTreeManifest(ctx, root, "", true, AllowAllFilter(), nil); err != nil {
		return nil, err
	}
	rehashTree(root)
	return merge.Flatten(root), nil
}

// rebaseCommits walks the first-parent history from head to stop (exclusive),
// oldest first.
func (b *Branch) rebaseCommits(ctx context.Context, projectID snow.ID, head snow.ID, stop *snow.ID) ([]*domain.Commit, error) {
	var out []*domain.Commit
	seen := map[snow.ID]bool{}
	current := head
	for {
		if stop != nil && current == *stop {
			break
		}
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
