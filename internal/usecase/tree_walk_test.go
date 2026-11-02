package usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

// memTreeRepo is an in-memory branchRepository that stores real, hashed trees.
// Every added tree gets fresh node ids, like separate tree_nodes rows that
// share a hash, and the repository counts directory loads.
type memTreeRepo struct {
	branchRepository
	nodes     map[int64]*domain.TreeNode
	files     map[int64][]*domain.File
	children  map[int64][]int64
	commits   map[snow.ID]*domain.Commit
	nextID    int64
	filesCall map[int64]int
	metaCall  int
	listErr   error
}

func newMemTreeRepo() *memTreeRepo {
	return &memTreeRepo{
		nodes:     map[int64]*domain.TreeNode{},
		files:     map[int64][]*domain.File{},
		children:  map[int64][]int64{},
		commits:   map[snow.ID]*domain.Commit{},
		filesCall: map[int64]int{},
	}
}

type dirSpec struct {
	files map[string]string
	dirs  map[string]*dirSpec
}

func specFromPaths(paths map[string]string) *dirSpec {
	root := &dirSpec{files: map[string]string{}, dirs: map[string]*dirSpec{}}
	for path, content := range paths {
		parts := strings.Split(path, "/")
		dir := root
		for _, part := range parts[:len(parts)-1] {
			next, ok := dir.dirs[part]
			if !ok {
				next = &dirSpec{files: map[string]string{}, dirs: map[string]*dirSpec{}}
				dir.dirs[part] = next
			}
			dir = next
		}
		dir.files[parts[len(parts)-1]] = content
	}
	return root
}

func (r *memTreeRepo) addDir(name string, spec *dirSpec) *domain.TreeNode {
	r.nextID++
	id := r.nextID

	fileNames := make([]string, 0, len(spec.files))
	for n := range spec.files {
		fileNames = append(fileNames, n)
	}
	sort.Strings(fileNames)
	var fileEntries []treehash.FileEntry
	var files []*domain.File
	for _, n := range fileNames {
		content := spec.files[n]
		chunk := chunker.Sum([]byte(content))
		fh := treehash.FileHash([]domain.Hash{chunk})
		fileEntries = append(fileEntries, treehash.FileEntry{Name: n, Hash: fh, Mode: 0o644})
		files = append(files, &domain.File{
			Name: n, Mode: 0o644, SizeBytes: int64(len(content)), Hash: fh,
			Chunks: []domain.Chunk{{Hash: chunk, SizeBytes: int64(len(content))}},
		})
	}

	dirNames := make([]string, 0, len(spec.dirs))
	for n := range spec.dirs {
		dirNames = append(dirNames, n)
	}
	sort.Strings(dirNames)
	var treeEntries []treehash.TreeEntry
	var kids []int64
	for _, n := range dirNames {
		child := r.addDir(n, spec.dirs[n])
		treeEntries = append(treeEntries, treehash.TreeEntry{Name: n, Hash: child.Hash})
		kids = append(kids, child.ID)
	}

	node := &domain.TreeNode{ID: id, Name: name, Hash: treehash.TreeHash(fileEntries, treeEntries)}
	r.nodes[id] = node
	r.files[id] = files
	r.children[id] = kids
	return node
}

func (r *memTreeRepo) addCommit(commitID snow.ID, projectID snow.ID, paths map[string]string) *domain.Commit {
	root := r.addDir("root", specFromPaths(paths))
	commit := &domain.Commit{ID: commitID, ProjectID: projectID, TreeID: root.ID}
	r.commits[commitID] = commit
	return commit
}

func (r *memTreeRepo) GetCommit(_ context.Context, id snow.ID) (*domain.Commit, error) {
	commit, ok := r.commits[id]
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	return commit, nil
}

func (r *memTreeRepo) GetTreeNode(_ context.Context, id int64) (*domain.TreeNode, error) {
	node, ok := r.nodes[id]
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	cp := *node
	return &cp, nil
}

func (r *memTreeRepo) ListTreeChildren(_ context.Context, id int64) ([]*domain.TreeNode, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]*domain.TreeNode, 0, len(r.children[id]))
	for _, kid := range r.children[id] {
		cp := *r.nodes[kid]
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memTreeRepo) ListFilesByTree(_ context.Context, id int64) ([]*domain.File, error) {
	r.filesCall[id]++
	out := make([]*domain.File, 0, len(r.files[id]))
	for _, f := range r.files[id] {
		cp := *f
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memTreeRepo) ListFileMetaByTree(_ context.Context, id int64) ([]*domain.File, error) {
	r.metaCall++
	files, _ := r.ListFilesByTree(context.Background(), id)
	r.filesCall[id]--
	for _, f := range files {
		f.Chunks = nil
	}
	return files, nil
}

func (r *memTreeRepo) loadedDirs() int {
	n := 0
	for _, c := range r.filesCall {
		n += c
	}
	return n
}

func restrictedReadFilter(prefixes ...string) *PathFilter {
	rules := make([]*domain.PBACRule, 0, len(prefixes))
	for _, p := range prefixes {
		rules = append(rules, &domain.PBACRule{PathPrefix: p, Permission: domain.PermissionRead})
	}
	return &PathFilter{set: &permissionSet{rules: rules}, permission: domain.PermissionRead}
}

func wideRepo(extra map[string]string) map[string]string {
	paths := map[string]string{}
	for _, dir := range []string{"assets", "code", "docs", "public"} {
		for i := 0; i < 3; i++ {
			paths[fmt.Sprintf("%s/f%d.txt", dir, i)] = fmt.Sprintf("%s content %d", dir, i)
			paths[fmt.Sprintf("%s/nested/n%d.txt", dir, i)] = fmt.Sprintf("%s nested %d", dir, i)
		}
	}
	paths["README.md"] = "readme"
	for k, v := range extra {
		if v == "" {
			delete(paths, k)
			continue
		}
		paths[k] = v
	}
	return paths
}

func fullLoadEntries(t *testing.T, uc *Branch, root *domain.TreeNode, filter *PathFilter) map[string]diff.Entry {
	t.Helper()
	require.NoError(t, uc.loadTreeManifest(context.Background(), root, "", true, filter, nil))
	return diff.FromTree(root)
}

func TestChangedTreeEntries_MatchesFullLoad(t *testing.T) {
	cases := []struct {
		name   string
		change map[string]string
		filter *PathFilter
		// loadedDirs is the number of ListFilesByTree calls the pruned walk may
		// make, unfiltered; -1 skips the check.
		loadedDirs int
	}{
		{name: "modify one file", change: map[string]string{"code/f1.txt": "changed"}, filter: AllowAllFilter(), loadedDirs: 4},
		{name: "add file in nested dir", change: map[string]string{"docs/nested/new.txt": "new"}, filter: AllowAllFilter(), loadedDirs: 6},
		{name: "delete file", change: map[string]string{"assets/f0.txt": ""}, filter: AllowAllFilter(), loadedDirs: 4},
		{name: "add new top-level dir", change: map[string]string{"extra/a/b.txt": "b"}, filter: AllowAllFilter(), loadedDirs: 4},
		{name: "rename across dirs", change: map[string]string{"code/f2.txt": "", "docs/f2.txt": "code content 2"}, filter: AllowAllFilter(), loadedDirs: 6},
		{name: "root file only", change: map[string]string{"README.md": "readme v2"}, filter: AllowAllFilter(), loadedDirs: 2},
		{name: "no change", change: nil, filter: AllowAllFilter(), loadedDirs: 0},
		{name: "restricted filter, change visible", change: map[string]string{"public/f1.txt": "changed"}, filter: restrictedReadFilter("public"), loadedDirs: -1},
		{name: "restricted filter, change hidden", change: map[string]string{"code/f1.txt": "changed"}, filter: restrictedReadFilter("public"), loadedDirs: -1},
		{name: "restricted filter, nested prefix", change: map[string]string{"docs/nested/n1.txt": "changed", "code/f0.txt": "changed"}, filter: restrictedReadFilter("docs/nested"), loadedDirs: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newMemTreeRepo()
			base := repo.addCommit(1, 1, wideRepo(nil))
			head := repo.addCommit(2, 1, wideRepo(tc.change))
			uc := NewBranch(NewMockpermissionUsecase(gomock.NewController(t)), repo, newTestBranchNode(t))

			baseRoot, err := repo.GetTreeNode(ctx, base.TreeID)
			require.NoError(t, err)
			headRoot, err := repo.GetTreeNode(ctx, head.TreeID)
			require.NoError(t, err)

			wantBase := fullLoadEntries(t, uc, mustRoot(t, repo, base.TreeID), tc.filter)
			wantHead := fullLoadEntries(t, uc, mustRoot(t, repo, head.TreeID), tc.filter)
			want := diff.Compare(wantBase, wantHead)

			repo.filesCall = map[int64]int{}
			gotBase, gotHead, err := uc.changedTreeEntries(ctx, baseRoot, headRoot, tc.filter)
			require.NoError(t, err)
			require.Equal(t, want, diff.Compare(gotBase, gotHead))
			if tc.loadedDirs >= 0 {
				require.Equal(t, tc.loadedDirs, repo.loadedDirs(), "directories loaded by the pruned walk")
			}
		})
	}
}

func mustRoot(t *testing.T, repo *memTreeRepo, id int64) *domain.TreeNode {
	t.Helper()
	root, err := repo.GetTreeNode(context.Background(), id)
	require.NoError(t, err)
	return root
}

func TestChangedTreeEntries_SameSubtreeAtTwoPaths(t *testing.T) {
	ctx := context.Background()
	repo := newMemTreeRepo()
	shared := map[string]string{"a/lib/x.txt": "same", "b/lib/x.txt": "same"}
	base := repo.addCommit(1, 1, shared)
	head := repo.addCommit(2, 1, map[string]string{"a/lib/x.txt": "same", "b/lib/x.txt": "different"})
	uc := NewBranch(NewMockpermissionUsecase(gomock.NewController(t)), repo, newTestBranchNode(t))

	// "a" is hidden, "b" is readable: identical stored subtrees must still be
	// judged per path.
	filter := restrictedReadFilter("b")
	baseEntries, headEntries, err := uc.changedTreeEntries(ctx, mustRoot(t, repo, base.TreeID), mustRoot(t, repo, head.TreeID), filter)
	require.NoError(t, err)
	changes := diff.Compare(baseEntries, headEntries)
	require.Len(t, changes, 1)
	require.Equal(t, "b/lib/x.txt", changes[0].Path)
	require.Equal(t, diff.Modified, changes[0].Status)
}

func TestChangedTreeEntries_NilBase(t *testing.T) {
	ctx := context.Background()
	repo := newMemTreeRepo()
	head := repo.addCommit(2, 1, wideRepo(nil))
	uc := NewBranch(NewMockpermissionUsecase(gomock.NewController(t)), repo, newTestBranchNode(t))

	baseEntries, headEntries, err := uc.changedTreeEntries(ctx, nil, mustRoot(t, repo, head.TreeID), AllowAllFilter())
	require.NoError(t, err)
	require.Empty(t, baseEntries)
	require.Len(t, headEntries, len(wideRepo(nil)))

	baseEntries, headEntries, err = uc.changedTreeEntries(ctx, nil, nil, AllowAllFilter())
	require.NoError(t, err)
	require.Empty(t, baseEntries)
	require.Empty(t, headEntries)
}

func TestChangedTreeEntries_ZeroHashIsNeverTreatedAsEqual(t *testing.T) {
	ctx := context.Background()
	repo := newMemTreeRepo()
	base := repo.addCommit(1, 1, map[string]string{"a.txt": "one"})
	head := repo.addCommit(2, 1, map[string]string{"a.txt": "two"})
	uc := NewBranch(NewMockpermissionUsecase(gomock.NewController(t)), repo, newTestBranchNode(t))

	baseRoot, headRoot := mustRoot(t, repo, base.TreeID), mustRoot(t, repo, head.TreeID)
	baseRoot.Hash, headRoot.Hash = domain.Hash{}, domain.Hash{}
	baseEntries, headEntries, err := uc.changedTreeEntries(ctx, baseRoot, headRoot, AllowAllFilter())
	require.NoError(t, err)
	require.Len(t, diff.Compare(baseEntries, headEntries), 1)
}

func TestChangedTreeEntries_RepositoryErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")

	t.Run("children", func(t *testing.T) {
		repo := newMemTreeRepo()
		base := repo.addCommit(1, 1, map[string]string{"a/x.txt": "1"})
		head := repo.addCommit(2, 1, map[string]string{"a/x.txt": "2"})
		repo.listErr = boom
		uc := NewBranch(NewMockpermissionUsecase(gomock.NewController(t)), repo, newTestBranchNode(t))
		_, _, err := uc.changedTreeEntries(ctx, mustRoot(t, repo, base.TreeID), mustRoot(t, repo, head.TreeID), AllowAllFilter())
		require.ErrorIs(t, err, boom)
	})

	t.Run("files", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := NewMockbranchRepository(ctrl)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(1)).Return(nil, boom)
		uc := NewBranch(NewMockpermissionUsecase(ctrl), repo, newTestBranchNode(t))
		_, _, err := uc.changedTreeEntries(ctx, &domain.TreeNode{ID: 1, Hash: domain.Hash{1}}, &domain.TreeNode{ID: 2, Hash: domain.Hash{2}}, AllowAllFilter())
		require.ErrorIs(t, err, boom)
	})

	t.Run("head files", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := NewMockbranchRepository(ctrl)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(1)).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(1)).Return(nil, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(2)).Return(nil, boom)
		uc := NewBranch(NewMockpermissionUsecase(ctrl), repo, newTestBranchNode(t))
		_, _, err := uc.changedTreeEntries(ctx, &domain.TreeNode{ID: 1, Hash: domain.Hash{1}}, &domain.TreeNode{ID: 2, Hash: domain.Hash{2}}, AllowAllFilter())
		require.ErrorIs(t, err, boom)
	})
}

func TestBranch_CommitChanges_SkipsIdenticalDirectories(t *testing.T) {
	ctx := context.Background()
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	repo.addCommit(2, 1, wideRepo(map[string]string{"code/nested/n0.txt": "changed"}))
	uc := NewBranch(newAllowAllPerm(gomock.NewController(t)), repo, newTestBranchNode(t))

	from, to := snow.ID(1), snow.ID(2)
	changes, err := uc.commitChanges(ctx, 1, &from, &to)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, "code/nested/n0.txt", changes[0].Path)
	// root, code and code/nested on each side
	require.Equal(t, 6, repo.loadedDirs())

	changes, err = uc.commitChanges(ctx, 1, nil, &to)
	require.NoError(t, err)
	require.Len(t, changes, len(wideRepo(map[string]string{"code/nested/n0.txt": "changed"})))
}

func visibleReference(t *testing.T, uc *Branch, repo *memTreeRepo, commits []snow.ID, filter *PathFilter, sparse domain.PrefixSet) []domain.Hash {
	t.Helper()
	seen := map[domain.Hash]struct{}{}
	for _, id := range commits {
		root := mustRoot(t, repo, repo.commits[id].TreeID)
		require.NoError(t, uc.loadTreeManifest(context.Background(), root, "", true, filter, sparse))
		var walk func(*domain.TreeNode)
		walk = func(n *domain.TreeNode) {
			for _, f := range n.FileChildren {
				for _, c := range f.Chunks {
					seen[c.Hash] = struct{}{}
				}
			}
			for _, c := range n.TreeChildren {
				walk(c)
			}
		}
		walk(root)
	}
	out := make([]domain.Hash, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i][:]) < string(out[j][:]) })
	return out
}

func TestBranch_VisibleChunks_MatchesFullLoadAcrossCommits(t *testing.T) {
	cases := []struct {
		name   string
		filter *PathFilter
		paths  []string
	}{
		{name: "everything", filter: AllowAllFilter()},
		{name: "restricted", filter: restrictedReadFilter("public", "docs/nested")},
		{name: "sparse", filter: AllowAllFilter(), paths: []string{"code", "docs/nested"}},
		{name: "restricted and sparse", filter: restrictedReadFilter("public", "code"), paths: []string{"code/nested"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newMemTreeRepo()
			repo.addCommit(1, 1, wideRepo(nil))
			repo.addCommit(2, 1, wideRepo(map[string]string{"code/f1.txt": "changed", "public/new.txt": "new"}))
			repo.addCommit(3, 1, wideRepo(map[string]string{"code/f1.txt": "changed", "public/new.txt": "new"}))

			ctrl := gomock.NewController(t)
			perm := NewMockpermissionUsecase(ctrl)
			perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
			perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(tc.filter, nil)
			uc := NewBranch(perm, repo, newTestBranchNode(t))

			sparse, err := domain.NewPrefixSet(tc.paths)
			require.NoError(t, err)
			want := visibleReference(t, uc, repo, []snow.ID{1, 2, 3}, tc.filter, sparse)

			repo.filesCall = map[int64]int{}
			got, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36(), snow.ID(2).Base36(), snow.ID(3).Base36()}, tc.paths)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestBranch_VisibleChunks_SkipsSharedSubtrees(t *testing.T) {
	ctx := context.Background()
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	repo.addCommit(2, 1, wideRepo(map[string]string{"code/f1.txt": "changed"}))

	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	_, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36(), snow.ID(2).Base36()}, nil)
	require.NoError(t, err)
	// first commit: root + 4 dirs + 4 nested = 9; second commit: root, code only
	require.Equal(t, 11, repo.loadedDirs())

	repo.filesCall = map[int64]int{}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
	_, err = uc.VisibleChunks(ctx, 1, []string{snow.ID(2).Base36(), snow.ID(2).Base36()}, nil)
	require.NoError(t, err)
	require.Equal(t, 9, repo.loadedDirs(), "the same commit twice is walked once")
}

func TestBranch_VisibleChunks_Errors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")

	t.Run("children error", func(t *testing.T) {
		repo := newMemTreeRepo()
		repo.addCommit(1, 1, map[string]string{"a/x.txt": "1"})
		repo.listErr = boom
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		uc := NewBranch(perm, repo, newTestBranchNode(t))
		_, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("files error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo := NewMockbranchRepository(ctrl)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(1)).Return(&domain.Commit{ID: 1, ProjectID: 1, TreeID: 5}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(5)).Return(&domain.TreeNode{ID: 5, Hash: domain.Hash{1}}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(5)).Return(nil, boom)
		uc := NewBranch(perm, repo, newTestBranchNode(t))
		_, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
		require.ErrorIs(t, err, boom)
	})
}

func TestBranch_CommitDiff_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")
	headID, baseID := snow.ID(11), snow.ID(10)
	head := &domain.Commit{ID: headID, ProjectID: 1, TreeID: 101, Parent1ID: &baseID}
	base := &domain.Commit{ID: baseID, ProjectID: 1, TreeID: 100}

	newUC := func(t *testing.T) (*Branch, *MockpermissionUsecase, *MockbranchRepository) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		return NewBranch(perm, repo, newTestBranchNode(t)), perm, repo
	}

	t.Run("head commit lookup", func(t *testing.T) {
		uc, _, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(nil, boom)
		_, _, err := uc.CommitDiff(ctx, 1, headID, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("head tree lookup", func(t *testing.T) {
		uc, _, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(nil, boom)
		_, _, err := uc.CommitDiff(ctx, 1, headID, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("base tree lookup", func(t *testing.T) {
		uc, _, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(base, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Hash: domain.Hash{1}}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(nil, boom)
		_, _, err := uc.CommitDiff(ctx, 1, headID, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("filter compile", func(t *testing.T) {
		uc, perm, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(base, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), gomock.Any()).Return(&domain.TreeNode{ID: 1, Hash: domain.Hash{1}}, nil).Times(2)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, boom)
		_, _, err := uc.CommitDiff(ctx, 1, headID, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("tree walk", func(t *testing.T) {
		uc, perm, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(base, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Hash: domain.Hash{1}}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Hash: domain.Hash{2}}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, boom)
		_, _, err := uc.CommitDiff(ctx, 1, headID, nil)
		require.ErrorIs(t, err, boom)
	})
}

func TestBranch_TreeDiffBetween_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")
	headID, baseID := snow.ID(11), snow.ID(10)
	head := &domain.Commit{ID: headID, ProjectID: 1, TreeID: 101}
	base := &domain.Commit{ID: baseID, ProjectID: 1, TreeID: 100}

	newUC := func(t *testing.T) (*Branch, *MockpermissionUsecase, *MockbranchRepository) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		return NewBranch(perm, repo, newTestBranchNode(t)), perm, repo
	}

	t.Run("base tree lookup", func(t *testing.T) {
		uc, _, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(base, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(nil, boom)
		_, err := uc.TreeDiffBetween(ctx, 1, &baseID, headID)
		require.ErrorIs(t, err, boom)
	})

	t.Run("head commit lookup", func(t *testing.T) {
		uc, _, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(nil, boom)
		_, err := uc.TreeDiffBetween(ctx, 1, nil, headID)
		require.ErrorIs(t, err, boom)
	})

	t.Run("head tree lookup", func(t *testing.T) {
		uc, _, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(nil, boom)
		_, err := uc.TreeDiffBetween(ctx, 1, nil, headID)
		require.ErrorIs(t, err, boom)
	})

	t.Run("filter compile", func(t *testing.T) {
		uc, perm, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Hash: domain.Hash{1}}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, boom)
		_, err := uc.TreeDiffBetween(ctx, 1, nil, headID)
		require.ErrorIs(t, err, boom)
	})

	t.Run("tree walk", func(t *testing.T) {
		uc, perm, repo := newUC(t)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(head, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Hash: domain.Hash{1}}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, boom)
		_, err := uc.TreeDiffBetween(ctx, 1, nil, headID)
		require.ErrorIs(t, err, boom)
	})
}
