package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// pathHistoryFixture pushes a linear history onto main of project 1 through
// the push usecase and reads it through a branch repository whose statements
// are counted (the postgres twin of the sqlite fixture).
type pathHistoryFixture struct {
	t       *testing.T
	ctx     context.Context
	counter *countingDBTX
	repo    *BranchRepository
	branch  *usecase.Branch
	push    *usecase.Push
	pushes  *PushRepository
	head    string
}

func newPathHistoryFixture(t *testing.T) *pathHistoryFixture {
	t.Helper()
	db, _ := newTestDB(t)
	counter := &countingDBTX{inner: dbtx.New(db)}
	repo := &BranchRepository{db: db, tx: dbtx.NewTransactor(db), queries: sqlcPostgres.New(counter)}
	pushes := NewPushRepository(db)
	node := newTestNode(t)
	return &pathHistoryFixture{
		t:       t,
		ctx:     domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1, IsSuperAdmin: true}),
		counter: counter,
		repo:    repo,
		branch:  usecase.NewBranch(allowAllPermissions{}, repo, node),
		push:    usecase.NewPush(allowAllPermissions{}, NewBranchRepository(db), pushes, node),
		pushes:  pushes,
	}
}

func (f *pathHistoryFixture) commit(files map[string]string, removed ...string) {
	f.t.Helper()
	var push []*domain.PushFile
	var refs []usecase.ChunkRef
	for path, content := range files {
		chunk := domain.Hash(sha256.Sum256([]byte(content)))
		refs = append(refs, usecase.ChunkRef{Hash: chunk, SizeBytes: int64(len(content))})
		push = append(push, &domain.PushFile{Path: path, Mode: 0o644, SizeBytes: int64(len(content)),
			FileHash: treehash.FileHash([]domain.Hash{chunk}), ChunkHashes: []domain.Hash{chunk}})
	}
	require.NoError(f.t, f.pushes.InsertChunksIfNotExist(f.ctx, 1, refs))
	res, err := f.push.Push(f.ctx, 1, "main", "", "change", push, removed, "", f.head)
	require.NoError(f.t, err)
	f.head = res.CommitID.Base36()
}

func (f *pathHistoryFixture) history(n int) {
	f.commit(map[string]string{"src/a.txt": "seed", "src/b.txt": "seed", "doc/readme": "seed"})
	for i := 1; i < n; i++ {
		if i%5 == 0 {
			f.commit(map[string]string{"src/a.txt": fmt.Sprintf("a %d", i)})
		} else {
			f.commit(map[string]string{fmt.Sprintf("other/f%d", i): "x"})
		}
	}
}

func (f *pathHistoryFixture) log(path string, limit int) ([]*domain.CommitLogEntry, int64) {
	f.t.Helper()
	f.counter.statements.Store(0)
	entries, err := f.branch.PathCommitLog(f.ctx, 1, "main", path, nil, limit)
	require.NoError(f.t, err)
	return entries, f.counter.statements.Load()
}

// TestPathCommitLog_StatementCountIsConstant pins the per-path history at
// three statements whatever the history length, for files and folders.
func (s *BranchRepositorySuite) TestPathCommitLog_StatementCountIsConstant() {
	t := s.T()
	for _, n := range []int{10, 80} {
		f := newPathHistoryFixture(t)
		f.history(n)

		got, statements := f.log("src/a.txt", 500)
		require.Len(t, got, 1+(n-1)/5)
		require.Equal(t, int64(3), statements, "file history over %d commits", n)

		got, statements = f.log("src", 500)
		require.Len(t, got, 1+(n-1)/5)
		require.Equal(t, int64(3), statements, "folder history over %d commits", n)
	}
}

// TestPathCommitLog_FolderRangeIsBytewise checks that a folder's rows are the
// byte range [folder/, folder0) even where the database's collation would
// sort punctuation differently: siblings named a-b or a.txt are not inside a.
func (s *BranchRepositorySuite) TestPathCommitLog_FolderRangeIsBytewise() {
	t := s.T()
	f := newPathHistoryFixture(t)
	var heads []snow.ID
	for _, files := range []map[string]string{
		{"a/in.txt": "seed"}, {"a-b/x.txt": "1"}, {"a.txt": "1"}, {"a/B/y.txt": "1"}, {"A/z.txt": "1"}, {"a0": "1"},
	} {
		f.commit(files)
		heads = append(heads, mustParseSnowID(t, f.head))
	}

	got, _ := f.log("a", 50)
	require.Equal(t, []snow.ID{heads[3], heads[0]}, []snow.ID{got[0].ID, got[1].ID})
	require.Len(t, got, 2, "only a/B/y.txt changed below a, and the root commit is listed")
}

func mustParseSnowID(t *testing.T, id string) snow.ID {
	t.Helper()
	parsed, err := snow.ParseBase36(id)
	require.NoError(t, err)
	return parsed
}

// TestPathChanges_LegacyAndRows indexes a history stored before the index on
// the first read and reads rows back for a path-restricted reader.
func (s *BranchRepositorySuite) TestPathChanges_LegacyAndRows() {
	t := s.T()
	f := newPathHistoryFixture(t)
	f.history(12)
	got, _ := f.log("src/a.txt", 500)
	require.Len(t, got, 3)

	_, err := f.repo.db.Exec(`DELETE FROM commit_path_changes WHERE commit_id > 0`)
	require.NoError(t, err)
	_, err = f.repo.db.Exec(`DELETE FROM commit_path_index WHERE commit_id > 0`)
	require.NoError(t, err)
	got, statements := f.log("src/a.txt", 500)
	require.Len(t, got, 3)
	require.Greater(t, statements, int64(3))
	got, statements = f.log("src/a.txt", 500)
	require.Len(t, got, 3)
	require.Equal(t, int64(3), statements)

	ctx := context.Background()
	head := mustParseSnowID(t, f.head)
	rows, err := f.repo.PathChangeRows(ctx, usecase.PathChangeQuery{Path: "other"}, nil)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, f.repo.RecordPathChanges(ctx, nil), "an empty batch writes nothing")
	var rootID int64
	require.NoError(t, f.repo.db.QueryRow(`SELECT id FROM commits WHERE parent_1_id IS NULL`).Scan(&rootID))
	root := snow.ID(rootID)
	require.NoError(t, f.repo.RecordPathChanges(ctx, []usecase.CommitPathChanges{
		{CommitID: head, Changes: []usecase.PathChange{{Path: "zz/new", Kind: usecase.PathChangeFile}}},
		{CommitID: root, Changes: []usecase.PathChange{{Path: "zz/root", Kind: usecase.PathChangeFile}}},
	}), "a batch stores several commits in one transaction")
	rootRows, err := f.repo.PathChangeRows(ctx, usecase.PathChangeQuery{Path: "zz"}, []snow.ID{root})
	require.NoError(t, err)
	require.Equal(t, []usecase.PathChangeRow{{CommitID: root, Path: "zz/root", Kind: usecase.PathChangeFile}}, rootRows)
	for path, want := range map[string][]string{
		"":       {"other/f11:1", "zz/new:1"},
		"other":  {"other/f11:1"},
		"zz/new": {"zz/new:1"},
	} {
		rows, err = f.repo.PathChangeRows(ctx, usecase.PathChangeQuery{Path: path}, []snow.ID{head, 12345})
		require.NoError(t, err)
		var paths []string
		for _, row := range rows {
			require.Equal(t, head, row.CommitID)
			paths = append(paths, fmt.Sprintf("%s:%d", row.Path, row.Kind))
		}
		require.Equal(t, want, paths, "path %q", path)
	}
}

// TestPathChanges_UnindexedDepthAndCutWalk checks that the unindexed lookup
// reports each commit's depth in the walk, newest first, and that a walk cut
// short by ScanLimit never lists its last commit as if it were the root.
func (s *BranchRepositorySuite) TestPathChanges_UnindexedDepthAndCutWalk() {
	t := s.T()
	f := newPathHistoryFixture(t)
	f.history(12)
	head := mustParseSnowID(t, f.head)
	_, err := f.repo.db.Exec(`DELETE FROM commit_path_index WHERE commit_id > 0`)
	require.NoError(t, err)

	pending, err := f.repo.UnindexedPathChangeCommits(f.ctx, 1, head, 10000)
	require.NoError(t, err)
	require.Len(t, pending, 11, "every commit but the root")
	depth := map[snow.ID]int{}
	for i, commit := range pending {
		require.Equal(t, i, commit.Depth)
		depth[commit.ID] = commit.Depth
	}
	capped, err := f.repo.UnindexedPathChangeCommits(f.ctx, 1, head, 4)
	require.NoError(t, err)
	require.Len(t, capped, 4)

	// src/a.txt changed at depths 1 and 6; the root is at depth 11.
	for scan, want := range map[int][]int{4: {1}, 7: {1, 6}, 11: {1, 6}, 12: {1, 6, 11}, 10000: {1, 6, 11}} {
		entries, err := f.repo.PathChangeLog(f.ctx, usecase.PathChangeQuery{ProjectID: 1, StartID: head, ScanLimit: scan, Path: "src/a.txt", Limit: 100})
		require.NoError(t, err)
		var depths []int
		for _, entry := range entries {
			if entry.Parent1ID == nil {
				depths = append(depths, len(pending))
				continue
			}
			depths = append(depths, depth[entry.ID])
		}
		require.Equal(t, want, depths, "scan limit %d", scan)
	}
}
