package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/snow"
)

func newStrategyBranch(t *testing.T) (*Branch, *MockpermissionUsecase, *MockbranchRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	return NewBranch(perm, repo, newTestBranchNode(t)), perm, repo
}

func TestMergeForMergeRequest_InvalidStrategy(t *testing.T) {
	uc, _, _ := newStrategyBranch(t)
	_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: "octopus"})
	requireUserError(t, err)
}

func TestMergeForMergeRequest_NoPermission(t *testing.T) {
	uc, perm, _ := newStrategyBranch(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)

	_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestMergeForMergeRequest_MissingBranches(t *testing.T) {
	t.Run("target not found", func(t *testing.T) {
		uc, perm, repo := newStrategyBranch(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("source not found", func(t *testing.T) {
		uc, perm, repo := newStrategyBranch(t)
		head := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
		require.True(t, domain.IsErrorNotFound(err))
	})
}

func TestMergeForMergeRequest_ProtectedTargetRequiresAdmin(t *testing.T) {
	uc, perm, repo := newStrategyBranch(t)
	targetHead := snow.ID(10)
	sourceHead := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestMergeForMergeRequest_EmptyAndEqualBranches(t *testing.T) {
	t.Run("source has no commits", func(t *testing.T) {
		uc, perm, repo := newStrategyBranch(t)
		targetHead := snow.ID(10)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}, nil)

		_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "no commits")
	})

	t.Run("target has no commits", func(t *testing.T) {
		uc, perm, repo := newStrategyBranch(t)
		sourceHead := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)

		_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "empty branch")
	})

	t.Run("same head", func(t *testing.T) {
		uc, perm, repo := newStrategyBranch(t)
		head := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &head}, nil)

		_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyMerge})
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "up to date")
	})
}

func TestMergeForMergeRequest_NotConfigured(t *testing.T) {
	uc, perm, repo := newStrategyBranch(t)
	targetHead := snow.ID(10)
	sourceHead := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)

	_, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategySquash})
	requireInternalServerError(t, err)
}

func requireInternalServerError(t *testing.T, err error) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func commitChain(repo *MockbranchRepository, commits ...*domain.Commit) {
	for _, c := range commits {
		repo.EXPECT().GetCommit(gomock.Any(), c.ID).Return(c, nil).AnyTimes()
	}
}

func chainCommit(id, parent snow.ID, message string) *domain.Commit {
	return &domain.Commit{ID: id, ProjectID: 1, Parent1ID: &parent, Message: message}
}

func TestBranch_RebaseCommits(t *testing.T) {
	t.Run("linear stops at the base", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		c1 := &domain.Commit{ID: 1, ProjectID: 1, Parent1ID: nil}
		c2 := chainCommit(2, 1, "two")
		c3 := chainCommit(3, 2, "three")
		commitChain(repo, c1, c2, c3)
		base := snow.ID(1)

		out, err := uc.rebaseCommits(context.Background(), snow.ID(1), 3, &base)
		require.NoError(t, err)
		require.Len(t, out, 2)
		require.Equal(t, snow.ID(2), out[0].ID)
		require.Equal(t, snow.ID(3), out[1].ID)
	})

	t.Run("base off the first-parent chain", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		a := &domain.Commit{ID: 1, ProjectID: 1}
		b := chainCommit(12, 1, "main")
		s1 := chainCommit(11, 1, "s1")
		m := &domain.Commit{ID: 10, ProjectID: 1, Parent1ID: ptr(snow.ID(11)), Parent2ID: ptr(snow.ID(12))}
		commitChain(repo, a, b, s1, m)
		base := snow.ID(12)

		out, err := uc.rebaseCommits(context.Background(), snow.ID(1), 10, &base)
		require.NoError(t, err)
		require.Len(t, out, 2)
		require.Equal(t, snow.ID(11), out[0].ID)
		require.Equal(t, snow.ID(10), out[1].ID)
	})

	t.Run("nil base replays to the root", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		c1 := &domain.Commit{ID: 1, ProjectID: 1}
		c2 := chainCommit(2, 1, "two")
		commitChain(repo, c1, c2)

		out, err := uc.rebaseCommits(context.Background(), snow.ID(1), 2, nil)
		require.NoError(t, err)
		require.Len(t, out, 2)
		require.Equal(t, snow.ID(1), out[0].ID)
	})

	t.Run("limit is enforced", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		commits := make([]*domain.Commit, 0, rebaseCommitLimit+2)
		for i := 0; i <= rebaseCommitLimit+1; i++ {
			commits = append(commits, &domain.Commit{ID: snow.ID(i + 1), ProjectID: 1})
		}
		for i := 0; i < len(commits)-1; i++ {
			commits[i].Parent1ID = ptr(commits[i+1].ID)
		}
		commitChain(repo, commits...)

		_, err := uc.rebaseCommits(context.Background(), snow.ID(1), snow.ID(1), nil)
		requireUserError(t, err)
		require.Contains(t, err.Error(), "cannot rebase more than")
	})

	t.Run("cycle is rejected", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		a := chainCommit(1, 2, "a")
		b := chainCommit(2, 1, "b")
		commitChain(repo, a, b)

		_, err := uc.rebaseCommits(context.Background(), snow.ID(1), 1, nil)
		requireInternalServerError(t, err)
	})

	t.Run("commit lookup error propagates", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		wantErr := errors.New("boom")
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(7)).Return(nil, wantErr)

		_, err := uc.rebaseCommits(context.Background(), snow.ID(1), 7, nil)
		require.ErrorIs(t, err, wantErr)
	})
}

func TestBranch_ReachableFrom(t *testing.T) {
	t.Run("nil start", func(t *testing.T) {
		uc, _, _ := newStrategyBranch(t)
		reached, err := uc.reachableFrom(context.Background(), snow.ID(1), nil)
		require.NoError(t, err)
		require.Empty(t, reached)
	})

	t.Run("follows both parents", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		root := &domain.Commit{ID: 4, ProjectID: 1}
		left := chainCommit(2, 4, "left")
		right := chainCommit(3, 4, "right")
		top := &domain.Commit{ID: 1, ProjectID: 1, Parent1ID: ptr(snow.ID(2)), Parent2ID: ptr(snow.ID(3))}
		commitChain(repo, root, left, right, top)
		start := snow.ID(1)

		reached, err := uc.reachableFrom(context.Background(), snow.ID(1), &start)
		require.NoError(t, err)
		require.Len(t, reached, 4)
		for _, id := range []snow.ID{1, 2, 3, 4} {
			require.True(t, reached[id])
		}
	})
}

func TestMergeDelta(t *testing.T) {
	unchanged := merge.File{Path: "same.txt", Mode: 1, Hash: domain.Hash{1}}
	changed := merge.File{Path: "changed.txt", Mode: 1, Hash: domain.Hash{2}}
	added := merge.File{Path: "added.txt", Mode: 1, Hash: domain.Hash{3}}
	removed := merge.File{Path: "removed.txt", Mode: 1, Hash: domain.Hash{4}}

	files, dropped := mergeDelta(
		map[string]merge.File{
			"same.txt":    unchanged,
			"changed.txt": {Path: "changed.txt", Mode: 1, Hash: domain.Hash{9}},
			"removed.txt": removed,
		},
		map[string]merge.File{
			"same.txt":    unchanged,
			"changed.txt": changed,
			"added.txt":   added,
		},
	)
	require.Len(t, files, 2)
	paths := []string{files[0].Path, files[1].Path}
	require.ElementsMatch(t, []string{"changed.txt", "added.txt"}, paths)
	require.Equal(t, []string{"removed.txt"}, dropped)
}

func TestEnsureMergedWrites(t *testing.T) {
	target := map[string]merge.File{"keep.txt": {Path: "keep.txt", Mode: 1, Hash: domain.Hash{1}}}
	merged := map[string]merge.File{"new.txt": {Path: "new.txt", Mode: 1, Hash: domain.Hash{2}}}

	t.Run("denied changed path", func(t *testing.T) {
		uc, perm, _ := newStrategyBranch(t)
		perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), "new.txt", domain.PermissionWrite).Return(false)
		require.True(t, domain.IsErrorNoPermission(uc.ensureMergedWrites(context.Background(), snow.ID(1), target, merged)))
	})

	t.Run("denied removal", func(t *testing.T) {
		uc, perm, _ := newStrategyBranch(t)
		perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), "new.txt", domain.PermissionWrite).Return(true)
		perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), "keep.txt", domain.PermissionWrite).Return(false)
		require.True(t, domain.IsErrorNoPermission(uc.ensureMergedWrites(context.Background(), snow.ID(1), target, merged)))
	})

	t.Run("unchanged paths are skipped", func(t *testing.T) {
		uc, perm, _ := newStrategyBranch(t)
		require.NoError(t, uc.ensureMergedWrites(context.Background(), snow.ID(1), target, target))
		_ = perm
	})
}

func TestConflictError(t *testing.T) {
	paths := make([]string, mergeConflictLimit+2)
	for i := range paths {
		paths[i] = fmt.Sprintf("f%02d.txt", i)
	}
	err := conflictError(paths, "commit abcde")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), "commit abcde: merge conflicts in")
	require.Contains(t, err.Error(), "(+2 more)")
	require.True(t, strings.HasPrefix(err.Error(), "commit abcde: "))

	plain := conflictError([]string{"b.txt", "a.txt"}, "")
	require.Contains(t, plain.Error(), "a.txt, b.txt")
}

type stubMergeCommitter struct{}

func (stubMergeCommitter) ApplyPushAll(context.Context, []ApplyPushRequest) error { return nil }

type stubChunkUploader struct{ err error }

func (u stubChunkUploader) Upload(context.Context, domain.Hash, []byte) (bool, error) {
	return false, u.err
}

func TestMergeForMergeRequest_RebaseEmptyReplayIsNoOp(t *testing.T) {
	uc, perm, repo := newStrategyBranch(t)
	uc.WithMergeCommitter(stubMergeCommitter{}).WithChunkUploader(stubChunkUploader{})
	uc.chunks = &stubChunkReader{err: errors.New("no chunks")}

	targetHead := snow.ID(10)
	sourceHead := snow.ID(11)
	targetCommit := &domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 5, Parent1ID: &sourceHead}
	sourceCommit := &domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 6}

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id snow.ID) (*domain.Commit, error) {
		switch id {
		case targetHead:
			return targetCommit, nil
		case sourceHead:
			return sourceCommit, nil
		}
		return nil, domain.NewErrorRecordNotFound()
	}).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(5)).Return(&domain.TreeNode{ID: 5, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(5)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(5)).Return(nil, nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil)

	branch, err := uc.MergeForMergeRequest(context.Background(), snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: domain.MergeStrategyRebase})
	require.NoError(t, err)
	require.Equal(t, snow.ID(2), branch.ID)
	require.Equal(t, &targetHead, branch.CommitID)
}

func TestMergeOnto_NoBase(t *testing.T) {
	uc, _, repo := newStrategyBranch(t)
	theirsID := snow.ID(11)
	repo.EXPECT().GetCommit(gomock.Any(), theirsID).Return(&domain.Commit{ID: theirsID, ProjectID: 1, TreeID: 5}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(5)).Return(&domain.TreeNode{ID: 5, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(5)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(5)).Return(nil, nil)

	current := map[string]merge.File{"keep.txt": {Path: "keep.txt", Mode: 1, Hash: domain.Hash{1}}}
	merged, err := uc.mergeOnto(context.Background(), snow.ID(1), nil, snow.ID(10), theirsID, current)
	require.NoError(t, err)
	require.Len(t, merged, 1)
	require.Contains(t, merged, "keep.txt")
}

func TestMergeTree_Errors(t *testing.T) {
	wantErr := errors.New("boom")
	const treeID = int64(5)

	t.Run("commit lookup", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).Return(nil, wantErr)
		_, err := uc.mergeTree(context.Background(), snow.ID(1), 11)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("tree lookup", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).Return(&domain.Commit{ID: 11, ProjectID: 1, TreeID: treeID}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), treeID).Return(nil, wantErr)
		_, err := uc.mergeTree(context.Background(), snow.ID(1), 11)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("files", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).Return(&domain.Commit{ID: 11, ProjectID: 1, TreeID: treeID}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), treeID).Return(&domain.TreeNode{ID: treeID}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), treeID).Return(nil, wantErr)
		_, err := uc.mergeTree(context.Background(), snow.ID(1), 11)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("children", func(t *testing.T) {
		uc, _, repo := newStrategyBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).Return(&domain.Commit{ID: 11, ProjectID: 1, TreeID: treeID}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), treeID).Return(&domain.TreeNode{ID: treeID}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), treeID).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), treeID).Return(nil, wantErr)
		_, err := uc.mergeTree(context.Background(), snow.ID(1), 11)
		require.ErrorIs(t, err, wantErr)
	})
}

func TestMaterialize_ContentErrors(t *testing.T) {
	chunkHash := domain.Hash{1}
	entry := merge.Entry{
		Decision: merge.TextMerge,
		Base:     merge.File{Path: "a.txt", Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{chunkHash}},
		Ours:     merge.File{Path: "a.txt", Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{chunkHash}},
		Theirs:   merge.File{Path: "a.txt", Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{chunkHash}},
	}

	uc, _, _ := newStrategyBranch(t)
	uc.chunks = &stubChunkReader{err: errors.New("chunk gone")}
	_, err := uc.materialize(context.Background(), &merge.Result{Entries: map[string]merge.Entry{"a.txt": entry}}, map[string]merge.File{})
	require.Error(t, err)

	uc.chunks = &stubChunkReader{data: map[domain.Hash][]byte{chunkHash: []byte("one\n")}}
	uc.WithChunkUploader(stubChunkUploader{err: errors.New("upload failed")})
	_, err = uc.materialize(context.Background(), &merge.Result{Entries: map[string]merge.Entry{"a.txt": entry}}, map[string]merge.File{})
	require.Error(t, err)
}

func TestStoreMergedContent_UploadError(t *testing.T) {
	uc, _, _ := newStrategyBranch(t)
	wantErr := errors.New("upload failed")
	uc.WithChunkUploader(stubChunkUploader{err: wantErr})

	_, err := uc.storeMergedContent(context.Background(), "a.txt", 1, []byte("hello\n"))
	require.ErrorIs(t, err, wantErr)
}

func ptr[T any](v T) *T { return &v }
