package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// clashTree is a stored tree for buildNewTree: directories by path with their
// file names. The mock answers only for the directories a build may read.
type clashTree map[string][]string

func (tree clashTree) repo(t *testing.T) (*MockbranchRepository, *domain.Commit) {
	t.Helper()
	repo := NewMockbranchRepository(gomock.NewController(t))
	ids := map[string]int64{}
	next := int64(100)
	id := func(dir string) int64 {
		if _, ok := ids[dir]; !ok {
			ids[dir] = next
			next++
		}
		return ids[dir]
	}
	for dir, names := range tree {
		var files []*domain.File
		for _, name := range names {
			files = append(files, &domain.File{Name: name, Mode: 0o644, Hash: domain.Hash{1}})
		}
		var children []*domain.TreeNode
		for sub := range tree {
			if sub != "" && dirOf(sub) == dir {
				children = append(children, &domain.TreeNode{ID: id(sub), Name: baseOf(sub), Hash: domain.Hash{byte(id(sub))}})
			}
		}
		repo.EXPECT().ListFilesByTree(gomock.Any(), id(dir)).Return(files, nil).AnyTimes()
		repo.EXPECT().ListTreeChildren(gomock.Any(), id(dir)).Return(children, nil).AnyTimes()
	}
	repo.EXPECT().GetTreeNode(gomock.Any(), id("")).Return(&domain.TreeNode{ID: id(""), Name: "root"}, nil).AnyTimes()
	return repo, &domain.Commit{ID: 9, TreeID: id("")}
}

func pushFilesAt(paths ...string) []*domain.PushFile {
	files := make([]*domain.PushFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, &domain.PushFile{Path: p, Mode: 0o644})
	}
	return files
}

var clashBaseTree = clashTree{
	"":        {"rootfile"},
	"a":       {"b"},
	"a/x":     {"one", "two"},
	"a/x/sub": {"deep"},
	"e":       {"only"},
}

func TestBuildNewTree_RefusesFileFolderClash(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		removed []string
		want    []string
	}{
		{name: "file over a kept folder", files: []string{"a/x"},
			want: []string{`"a/x" is written as a file, but the folder "a/x" still holds files that are not deleted`}},
		{name: "file over a rebuilt folder", files: []string{"a/x"}, removed: []string{"a/x/one"},
			want: []string{`"a/x" is written as a file, but the folder "a/x" still holds files`}},
		{name: "file over a folder keeping a subfolder", files: []string{"a/x"}, removed: []string{"a/x/one", "a/x/two"},
			want: []string{`"a/x" is written as a file, but the folder "a/x" still holds files`}},
		{name: "file over a root folder", files: []string{"e"},
			want: []string{`"e" is written as a file, but the folder "e" still holds files`}},
		{name: "file below a file", files: []string{"a/b/c"},
			want: []string{`files are written below "a/b", but "a/b" is a file that is not deleted`}},
		{name: "file below a root file", files: []string{"rootfile/c/d"},
			want: []string{`files are written below "rootfile", but "rootfile" is a file that is not deleted`}},
		{name: "file and files below it", files: []string{"n/p", "n/p/q"},
			want: []string{`"n/p" is written as a file and files are written below it`}},
		{name: "all sorted by path", files: []string{"rootfile/c", "a/x", "a/b/c"},
			want: []string{`files are written below "a/b", but "a/b" is a file that is not deleted; "a/x" is written as a file, but the folder "a/x" still holds files that are not deleted; files are written below "rootfile"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, head := clashBaseTree.repo(t)
			root, err := buildNewTree(context.Background(), repo, head, pushFilesAt(tc.files...), tc.removed)
			require.Nil(t, root)
			require.Error(t, err)
			var userErr *domain.Error
			require.True(t, errors.As(err, &userErr))
			require.Equal(t, 400, userErr.Code)
			require.Contains(t, err.Error(), "a path cannot be both a file and a folder")
			require.Contains(t, err.Error(), "stage the deletions too")
			for _, want := range tc.want {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestBuildNewTree_AllowsSwaps(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		removed []string
	}{
		{name: "folder to file", files: []string{"a/x"}, removed: []string{"a/x/one", "a/x/two", "a/x/sub/deep"}},
		{name: "root folder to file", files: []string{"e"}, removed: []string{"e/only"}},
		{name: "file to folder", files: []string{"a/b/c", "rootfile/d/e"}, removed: []string{"a/b", "rootfile"}},
		{name: "plain edits", files: []string{"a/b", "a/x/one", "a/x/new", "z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, head := clashBaseTree.repo(t)
			root, err := buildNewTree(context.Background(), repo, head, pushFilesAt(tc.files...), tc.removed)
			require.NoError(t, err)
			require.NotNil(t, root)
		})
	}
}

// A base tree that already holds a clash (stored before the check) blocks a
// push that rebuilds the directory without repairing it, and lets the repair
// through.
func TestBuildNewTree_DamagedBase(t *testing.T) {
	damaged := clashTree{"": nil, "a": {"x"}, "a/x": {"one"}}
	repo, head := damaged.repo(t)
	_, err := buildNewTree(context.Background(), repo, head, pushFilesAt("a/z"), nil)
	require.ErrorContains(t, err, `"a/x" already is a file and a folder in the base commit`)

	_, err = buildNewTree(context.Background(), repo, head, nil, []string{"a/x"})
	require.NoError(t, err)
	_, err = buildNewTree(context.Background(), repo, head, nil, []string{"a/x/one"})
	require.NoError(t, err)
	_, err = buildNewTree(context.Background(), repo, head, pushFilesAt("elsewhere"), nil)
	require.NoError(t, err, "a directory that is not rebuilt is not read")
}

func TestBuildNewTree_ClashListIsCapped(t *testing.T) {
	tree := clashTree{"": nil}
	var files []string
	for i := 0; i < treeClashLimit+5; i++ {
		tree[""] = append(tree[""], fmt.Sprintf("f%02d", i))
		files = append(files, fmt.Sprintf("f%02d/below", i))
	}
	repo, head := tree.repo(t)
	_, err := buildNewTree(context.Background(), repo, head, pushFilesAt(files...), nil)
	require.ErrorContains(t, err, `"f19"`)
	require.ErrorContains(t, err, "(+5 more)")
	require.NotContains(t, err.Error(), `"f20"`)

	merged := mergeTreeClash(err, "commit abc")
	require.True(t, domain.IsErrorConflict(merged))
	require.ErrorContains(t, merged, "commit abc: merge conflicts in f00, f01")
	require.ErrorContains(t, merged, fmt.Sprintf("(+%d more)", treeClashLimit+5-mergeConflictLimit))
	require.ErrorContains(t, merged, "one branch has the path as a file, the other as a folder")
}

func TestMergeTreeClash_PassesOtherErrorsThrough(t *testing.T) {
	other := domain.NewErrorDatabase("boom")
	require.Same(t, error(other), mergeTreeClash(other, ""))

	repo, head := clashBaseTree.repo(t)
	_, err := buildNewTree(context.Background(), repo, head, pushFilesAt("a/x"), nil)
	merged := mergeTreeClash(err, "")
	require.True(t, domain.IsErrorConflict(merged))
	require.ErrorContains(t, merged, "merge conflicts in a/x: one branch")
}

// Paths reach the builder validated; one that is not (a leading slash) must
// not make it store the same directory twice.
func TestBuildNewTree_RefusesADirectoryBuiltTwice(t *testing.T) {
	repo, head := clashTree{"": {"keep"}}.repo(t)
	_, err := buildNewTree(context.Background(), repo, head, pushFilesAt("d/one", "/d/two"), nil)
	var internal *domain.Error
	require.True(t, errors.As(err, &internal))
	require.Equal(t, 500, internal.Code)
	require.Contains(t, internal.InternalMessage, `directory "d" is built twice`)

	repo, head = clashTree{"": nil}.repo(t)
	_, err = buildNewTree(context.Background(), repo, head, pushFilesAt("d/one", "/d/two"), nil)
	require.True(t, domain.IsServerError(err), "also in a directory without files")
}

func TestPush_RefusesFileFolderClashBeforeWriting(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: idPtr(snow.ID(9))}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).Return(&domain.Commit{ID: 9, TreeID: 100, Hash: domain.Hash{5}}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).
		Return([]*domain.TreeNode{{ID: 101, Name: "x", Hash: domain.Hash{6}}}, nil)

	ch, fh := chunkAndFileHash(t, "hello")
	file := &domain.PushFile{Path: "x", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	// No ApplyPush expectation: the refusal comes before any write.
	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil, "", idPtr(snow.ID(9)).Base36())
	require.ErrorContains(t, err, `"x" is written as a file, but the folder "x" still holds files that are not deleted`)
	require.False(t, domain.IsServerError(err))
}

func TestPush_RefusesAbsolutePaths(t *testing.T) {
	for _, tc := range []struct {
		files   []string
		removed []string
	}{
		{files: []string{"/a/b"}},
		{files: []string{"/a"}},
		{removed: []string{"/a/b"}},
	} {
		uc, perm, _, _, ctx := newPushFixture(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", pushFilesAt(tc.files...), tc.removed, "", "")
		require.ErrorContains(t, err, "invalid file path")
	}
}

func TestImportCommit_RefusesFileFolderClash(t *testing.T) {
	f := newImportFixture(t)
	f.admin()
	parent := storedCommit(5, gitOrigin(2))
	f.branches.EXPECT().CommitsByOrigins(gomock.Any(), importProject, []string{gitOrigin(1), gitOrigin(2)}).
		Return([]*domain.Commit{parent}, nil)
	f.branches.EXPECT().GetTreeNode(gomock.Any(), int64(50)).Return(&domain.TreeNode{ID: 50, Name: "root"}, nil)
	f.branches.EXPECT().ListFilesByTree(gomock.Any(), int64(50)).Return([]*domain.File{{Name: "keep", Mode: 0o644, Hash: domain.Hash{7}}}, nil)
	f.branches.EXPECT().ListTreeChildren(gomock.Any(), int64(50)).Return(nil, nil)
	// No ApplyImport expectation: nothing is written.
	_, err := f.uc.Commit(importCtx(), importProject, ImportCommit{
		Origin:        gitOrigin(1),
		ParentOrigins: []string{gitOrigin(2)},
		Message:       "clash",
		Files:         []*domain.PushFile{importFile(t, "keep/below.txt", "b")},
	})
	require.ErrorContains(t, err, `files are written below "keep", but "keep" is a file that is not deleted`)
}

func TestValidatePushPaths_RefuseDuplicates(t *testing.T) {
	require.ErrorContains(t, validatePushFiles(pushFilesAt("a/b", "c", "a/b")), `duplicate file path "a/b"`)
	require.NoError(t, validatePushFiles(pushFilesAt("a/b", "a/B")), "names differing only in case are distinct paths")
	require.ErrorContains(t, validateRemovedFiles([]string{"a/b", "a/b"}, nil), `duplicate removed path "a/b"`)
	require.ErrorContains(t, validateRemovedFiles([]string{"a/b"}, pushFilesAt("a/b")), `is both pushed and removed`)
}
