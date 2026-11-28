package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// pathHistoryFixture pushes a linear history onto main of project 1 through
// the push usecase and reads it through a branch repository whose statements
// are counted.
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
	db, _ := newSQLiteTestDB(t)
	counter := &countingDBTX{inner: dbtx.New(db)}
	repo := &BranchRepository{db: db, tx: dbtx.NewTransactor(db), queries: sqlcSqlite.New(counter)}
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

func (f *pathHistoryFixture) commit(files map[string]string) {
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
	res, err := f.push.Push(f.ctx, 1, "main", "", "change", push, nil, "", f.head)
	require.NoError(f.t, err)
	f.head = res.CommitID.Base36()
}

// history pushes n commits: every fifth changes src/a.txt, the others
// another file each.
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

func (f *pathHistoryFixture) log(path string, limit int) (int, int64) {
	f.t.Helper()
	f.counter.statements.Store(0)
	entries, err := f.branch.PathCommitLog(f.ctx, 1, "main", path, nil, limit)
	require.NoError(f.t, err)
	return len(entries), f.counter.statements.Load()
}

// TestPathCommitLog_StatementCountIsConstant pins the per-path history at
// three statements (resolve the branch, look for unindexed commits, read the
// index) whatever the history length and whether a file or a folder is
// asked for.
func TestPathCommitLog_StatementCountIsConstant(t *testing.T) {
	for _, n := range []int{10, 150} {
		f := newPathHistoryFixture(t)
		f.history(n)

		got, statements := f.log("src/a.txt", 500)
		require.Equal(t, 1+(n-1)/5, got, "the seed commit and every fifth commit")
		require.Equal(t, int64(3), statements, "file history over %d commits", n)

		got, statements = f.log("src", 500)
		require.Equal(t, 1+(n-1)/5, got)
		require.Equal(t, int64(3), statements, "folder history over %d commits", n)

		got, statements = f.log("other", 3)
		require.Equal(t, 3, got)
		require.Equal(t, int64(3), statements)
	}
}

// TestPathCommitLog_IndexesLegacyCommitsOnce drops the index of a history
// stored before it existed: the first read indexes every commit of its walk
// and answers the same, the next read costs three statements again.
func TestPathCommitLog_IndexesLegacyCommitsOnce(t *testing.T) {
	f := newPathHistoryFixture(t)
	f.history(30)
	got, _ := f.log("src/a.txt", 500)
	require.Equal(t, 6, got)

	_, err := f.repo.db.Exec(`DELETE FROM commit_path_changes WHERE commit_id > 0`)
	require.NoError(t, err)
	_, err = f.repo.db.Exec(`DELETE FROM commit_path_index WHERE commit_id > 0`)
	require.NoError(t, err)

	got, statements := f.log("src/a.txt", 500)
	require.Equal(t, 6, got)
	require.Greater(t, statements, int64(3))
	pending, err := f.repo.UnindexedPathChangeCommits(f.ctx, 1, mustParseSnow(t, f.head), 10000)
	require.NoError(t, err)
	require.Empty(t, pending)

	got, statements = f.log("src/a.txt", 500)
	require.Equal(t, 6, got)
	require.Equal(t, int64(3), statements)
}

// TestBranchRepository_UnindexedDepthAndCutWalk checks that the unindexed
// lookup reports each commit's depth in the walk, newest first, and that a
// walk cut short by ScanLimit lists only the commits that changed the path,
// never its last commit as if it were the root.
func TestBranchRepository_UnindexedDepthAndCutWalk(t *testing.T) {
	f := newPathHistoryFixture(t)
	f.history(12)
	head := mustParseSnow(t, f.head)
	_, err := f.repo.db.Exec(`DELETE FROM commit_path_index WHERE commit_id > 0`)
	require.NoError(t, err)

	pending, err := f.repo.UnindexedPathChangeCommits(f.ctx, 1, head, 10000)
	require.NoError(t, err)
	require.Len(t, pending, 11, "every commit but the root")
	for i, commit := range pending {
		require.Equal(t, i, commit.Depth)
	}
	capped, err := f.repo.UnindexedPathChangeCommits(f.ctx, 1, head, 4)
	require.NoError(t, err)
	require.Len(t, capped, 4)

	// Commits at depth 1 (i=10) and 6 (i=5) changed src/a.txt; the root is
	// at depth 11. A walk cut at 4 commits holds only depth 1; one cut at 11
	// stops just above the root and must not list depth 10 as a root.
	for scan, want := range map[int][]int{4: {1}, 7: {1, 6}, 11: {1, 6}, 12: {1, 6, 11}, 10000: {1, 6, 11}} {
		entries, err := f.repo.PathChangeLog(f.ctx, usecase.PathChangeQuery{ProjectID: 1, StartID: head, ScanLimit: scan, Path: "src/a.txt", Limit: 100})
		require.NoError(t, err)
		var depths []int
		for _, entry := range entries {
			for _, commit := range pending {
				if commit.ID == entry.ID {
					depths = append(depths, commit.Depth)
				}
			}
			if entry.Parent1ID == nil {
				depths = append(depths, len(pending))
			}
		}
		require.Equal(t, want, depths, "scan limit %d", scan)
	}
}

func mustParseSnow(t *testing.T, id string) snow.ID {
	t.Helper()
	parsed, err := snow.ParseBase36(id)
	require.NoError(t, err)
	return parsed
}

func TestBranchRepository_PathChanges(t *testing.T) {
	f := newPathHistoryFixture(t)
	f.history(2)
	head := mustParseSnow(t, f.head)
	ctx := context.Background()

	rows, err := f.repo.PathChangeRows(ctx, usecase.PathChangeQuery{Path: "other"}, nil)
	require.NoError(t, err)
	require.Empty(t, rows)

	require.NoError(t, f.repo.RecordPathChanges(ctx, nil), "an empty batch writes nothing")
	// Recording again keeps what is there and adds what is new; a batch
	// stores several commits (and their markers) in one transaction.
	var rootID int64
	require.NoError(t, f.repo.db.QueryRow(`SELECT id FROM commits WHERE parent_1_id IS NULL`).Scan(&rootID))
	root := snow.ID(rootID)
	require.NoError(t, f.repo.RecordPathChanges(ctx, []usecase.CommitPathChanges{
		{CommitID: head, Changes: []usecase.PathChange{
			{Path: "other/f1", Kind: usecase.PathChangeFile},
			{Path: "zz/new", Kind: usecase.PathChangeFile},
			{Path: "zz/new", Kind: usecase.PathChangeFile},
		}},
		{CommitID: root, Changes: []usecase.PathChange{{Path: "zz/root", Kind: usecase.PathChangeFile}}},
	}))
	rootRows, err := f.repo.PathChangeRows(ctx, usecase.PathChangeQuery{Path: "zz"}, []snow.ID{root})
	require.NoError(t, err)
	require.Equal(t, []usecase.PathChangeRow{{CommitID: root, Path: "zz/root", Kind: usecase.PathChangeFile}}, rootRows)
	var marked int
	require.NoError(t, f.repo.db.QueryRow(`SELECT COUNT(*) FROM commit_path_index WHERE commit_id = ?`, root.Int64()).Scan(&marked))
	require.Equal(t, 1, marked)
	for _, q := range []usecase.PathChangeQuery{{Path: ""}, {Path: "other"}, {Path: "zz/new"}} {
		rows, err = f.repo.PathChangeRows(ctx, q, []snow.ID{head, 12345})
		require.NoError(t, err)
		var paths []string
		for _, row := range rows {
			require.Equal(t, head, row.CommitID)
			paths = append(paths, fmt.Sprintf("%s:%d", row.Path, row.Kind))
		}
		switch q.Path {
		case "other":
			require.Equal(t, []string{"other:3", "other/f1:1"}, paths)
		case "zz/new":
			require.Equal(t, []string{"zz/new:1"}, paths)
		default:
			require.Equal(t, []string{"other:3", "other/f1:1", "zz/new:1"}, paths)
		}
	}
}
