package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

func gitOrigin(c string) string {
	return "git:" + strings.Repeat(c, 40)
}

type importFixture struct {
	repo      *PushRepository
	branches  *BranchRepository
	db        *sql.DB
	projectID snow.ID
	branchID  snow.ID
}

func newImportFixture(t *testing.T) importFixture {
	t.Helper()
	repo, branches, db, projectID, branchID := newPushTestEnv(t)
	return importFixture{repo: repo, branches: branches, db: db, projectID: projectID, branchID: branchID}
}

func (f importFixture) linkChunk(t *testing.T, data string) (domain.Hash, int64, domain.Hash) {
	t.Helper()
	ch, sz, fh := chunkRow(t, data)
	ctx := context.Background()
	require.NoError(t, f.repo.InsertChunkIfNotExists(ctx, ch, sz))
	require.NoError(t, f.repo.LinkProjectChunk(ctx, f.projectID, ch))
	return ch, sz, fh
}

func rootImport(projectID snow.ID, id snow.ID, origin string, name string, ch domain.Hash, sz int64, fh domain.Hash) usecase.ApplyPushRequest {
	rootHash := treehash.TreeHash([]treehash.FileEntry{{Name: name, Mode: 0o644, Hash: fh}}, nil)
	authored := time.Date(2020, 5, 17, 10, 30, 0, 0, time.UTC)
	return usecase.ApplyPushRequest{
		ProjectID:   projectID,
		CommitID:    id,
		CommitHash:  treehash.CommitHashWithOrigin(rootHash, nil, "import "+name, origin),
		UserID:      snow.ID(1),
		Message:     "import " + name,
		Origin:      origin,
		AuthorName:  "Ada Lovelace",
		AuthorEmail: "ada@example.com",
		CreatedAt:   &authored,
		Nodes:       []usecase.PushNodeRow{{ID: 1, Hash: rootHash, Name: "root", Mode: 0o444}},
		Files:       []usecase.PushFileRow{{Name: name, Mode: 0o644, SizeBytes: sz, Hash: fh, TreeID: 1, ChunkHashes: []domain.Hash{ch}}},
	}
}

func TestPushRepositorySQLite_ApplyImportRoot(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch, sz, fh := f.linkChunk(t, "root")

	req := rootImport(f.projectID, snow.ID(10), gitOrigin("a"), "a.txt", ch, sz, fh)
	req.BranchID = f.branchID
	commit, err := f.repo.ApplyImport(ctx, req)
	require.NoError(t, err)
	require.Equal(t, snow.ID(10), commit.ID)
	require.Equal(t, req.CommitHash, commit.Hash)
	require.Equal(t, gitOrigin("a"), commit.Origin)
	require.Equal(t, "Ada Lovelace", commit.AuthorName)
	require.Equal(t, "ada@example.com", commit.AuthorEmail)
	require.True(t, req.CreatedAt.Equal(commit.CreatedAt))
	require.Nil(t, commit.Parent1ID)

	branch := getBranch(t, f.branches, f.projectID, f.branchID)
	require.Nil(t, branch.CommitID, "an import never moves a branch")

	stored, err := f.branches.GetCommit(ctx, commit.ID)
	require.NoError(t, err)
	require.Equal(t, commit, stored)
	byHash, err := f.branches.GetCommitByHash(ctx, f.projectID, commit.Hash)
	require.NoError(t, err)
	require.Equal(t, gitOrigin("a"), byHash.Origin)
	log, err := f.branches.CommitLog(ctx, f.projectID, commit.ID, 10)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, "Ada Lovelace", log[0].Commit.AuthorName)
	require.Equal(t, "ada@example.com", log[0].Commit.AuthorEmail)
	require.Equal(t, gitOrigin("a"), log[0].Origin)
	require.True(t, req.CreatedAt.Equal(log[0].CreatedAt))
	until, err := f.branches.CommitLogUntil(ctx, f.projectID, commit.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, until, 1)
	require.Equal(t, log[0].Commit, until[0].Commit)
	files := getFilesByTree(t, f.branches, getTreeNode(t, f.branches, commit.TreeID).ID)
	require.Len(t, files, 1)
	require.Equal(t, "a.txt", files[0].Name)
}

func TestPushRepositorySQLite_ApplyImportChildAndMerge(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch1, sz1, fh1 := f.linkChunk(t, "one")
	ch2, sz2, fh2 := f.linkChunk(t, "two")

	root, err := f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), gitOrigin("1"), "a.txt", ch1, sz1, fh1))
	require.NoError(t, err)
	side, err := f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(2), gitOrigin("2"), "b.txt", ch2, sz2, fh2))
	require.NoError(t, err)

	dirHash := treehash.TreeHash([]treehash.FileEntry{{Name: "b.txt", Mode: 0o644, Hash: fh2}}, nil)
	rootFiles := []treehash.FileEntry{{Name: "a.txt", Mode: 0o644, Hash: fh1}}
	childHash := treehash.TreeHash(rootFiles, []treehash.TreeEntry{{Name: "dir", Hash: dirHash}})
	parent := root.ID
	child, err := f.repo.ApplyImport(ctx, usecase.ApplyPushRequest{
		ProjectID:  f.projectID,
		CommitID:   snow.ID(3),
		CommitHash: treehash.CommitHashWithOrigin(childHash, []domain.Hash{root.Hash}, "child", gitOrigin("3")),
		ParentID:   &parent,
		UserID:     snow.ID(1),
		Message:    "child",
		Origin:     gitOrigin("3"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: childHash, Name: "root", Mode: 0o444},
			{ID: 2, Hash: dirHash, Name: "dir", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "a.txt", Mode: 0o644, SizeBytes: sz1, Hash: fh1, TreeID: 1, ChunkHashes: []domain.Hash{ch1}},
			{Name: "b.txt", Mode: 0o644, SizeBytes: sz2, Hash: fh2, TreeID: 2, ChunkHashes: []domain.Hash{ch2}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, root.ID, *child.Parent1ID)
	require.Nil(t, child.Parent2ID)
	require.Empty(t, child.AuthorName)
	require.WithinDuration(t, time.Now(), child.CreatedAt, time.Minute)
	childRoot := getTreeNode(t, f.branches, child.TreeID)
	require.Len(t, getFilesByTree(t, f.branches, getTreeChildByName(t, f.branches, childRoot.ID, "dir").ID), 1)

	p1, p2 := child.ID, side.ID
	merge, err := f.repo.ApplyImport(ctx, usecase.ApplyPushRequest{
		ProjectID:  f.projectID,
		CommitID:   snow.ID(4),
		CommitHash: treehash.CommitHashWithOrigin(childHash, []domain.Hash{child.Hash, side.Hash}, "merge", gitOrigin("4")),
		ParentID:   &p1,
		ParentID2:  &p2,
		UserID:     snow.ID(1),
		Message:    "merge",
		Origin:     gitOrigin("4"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: childHash, Name: "root", Mode: 0o444},
			{ID: 2, Hash: dirHash, Name: "dir", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{{Name: "a.txt", Mode: 0o644, SizeBytes: sz1, Hash: fh1, TreeID: 1, ChunkHashes: []domain.Hash{ch1}}},
	})
	require.NoError(t, err)
	require.Equal(t, child.ID, *merge.Parent1ID)
	require.Equal(t, side.ID, *merge.Parent2ID)

	ancestors, err := f.branches.CommitAncestors(ctx, f.projectID, merge.ID, 10)
	require.NoError(t, err)
	require.Len(t, ancestors, 4)
	require.Nil(t, getBranch(t, f.branches, f.projectID, f.branchID).CommitID)
}

func TestPushRepositorySQLite_ApplyImportDuplicateOrigin(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch, sz, fh := f.linkChunk(t, "dup")

	first, err := f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), gitOrigin("d"), "a.txt", ch, sz, fh))
	require.NoError(t, err)

	retry := rootImport(f.projectID, snow.ID(2), gitOrigin("d"), "other.txt", ch, sz, fh)
	again, err := f.repo.ApplyImport(ctx, retry)
	require.NoError(t, err)
	require.Equal(t, first, again)

	_, err = f.branches.GetCommit(ctx, snow.ID(2))
	require.True(t, domain.IsErrorNotFound(err), "a duplicate origin writes nothing")
	var nodes int
	require.NoError(t, f.db.QueryRow("SELECT COUNT(*) FROM tree_nodes").Scan(&nodes))
	require.Equal(t, 1, nodes)
}

func TestPushRepositorySQLite_ApplyImportConcurrentSameOrigin(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch, sz, fh := f.linkChunk(t, "race")

	var wg sync.WaitGroup
	results := make([]*domain.Commit, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(100+i), gitOrigin("r"), "a.txt", ch, sz, fh))
		}(i)
	}
	wg.Wait()
	for i := range results {
		require.NoError(t, errs[i])
		require.Equal(t, results[0].ID, results[i].ID)
	}
}

func TestPushRepositorySQLite_ApplyImportErrors(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch, sz, fh := chunkRow(t, "unlinked")

	_, err := f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), gitOrigin("m"), "a.txt", ch, sz, fh))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing chunk data")

	_, err = f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), "", "a.txt", ch, sz, fh))
	require.Error(t, err)

	ch, sz, fh = f.linkChunk(t, "linked")
	_, err = f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), gitOrigin("x"), "a.txt", ch, sz, fh))
	require.NoError(t, err)
	_, err = f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), gitOrigin("y"), "b.txt", ch, sz, fh))
	require.True(t, domain.IsErrorConflict(err), "a clash that is not the origin stays an error")
}

func TestPushRepositorySQLite_ApplyPushLeavesOriginEmpty(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch, sz, fh := f.linkChunk(t, "native")

	req := rootImport(f.projectID, snow.ID(1), "", "a.txt", ch, sz, fh)
	req.BranchID = f.branchID
	req.AuthorName, req.AuthorEmail, req.CreatedAt = "", "", nil
	require.NoError(t, f.repo.ApplyPush(ctx, req))

	commit, err := f.branches.GetCommit(ctx, snow.ID(1))
	require.NoError(t, err)
	require.Empty(t, commit.Origin)
	require.Empty(t, commit.AuthorName)
	require.Empty(t, commit.AuthorEmail)
	require.WithinDuration(t, time.Now(), commit.CreatedAt, time.Minute)
	require.Equal(t, snow.ID(1), *getBranch(t, f.branches, f.projectID, f.branchID).CommitID)

	var origin sql.NullString
	require.NoError(t, f.db.QueryRow("SELECT origin FROM commits WHERE id = ?", 1).Scan(&origin))
	require.False(t, origin.Valid)

	log, err := f.branches.CommitLog(ctx, f.projectID, snow.ID(1), 10)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, "Super Admin", log[0].AuthorName)
	require.Empty(t, log[0].Commit.AuthorName)

	req2 := rootImport(f.projectID, snow.ID(2), "", "b.txt", ch, sz, fh)
	parent := snow.ID(1)
	req2.ParentID = &parent
	require.NoError(t, f.repo.ApplyPush(ctx, req2))
	require.Equal(t, snow.ID(1), *getBranch(t, f.branches, f.projectID, f.branchID).CommitID, "a zero branch id moves no head")
}

func TestBranchRepositorySQLite_CommitsByOrigins(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	ch, sz, fh := f.linkChunk(t, "scoped")
	_, err := f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(1), gitOrigin("a"), "a.txt", ch, sz, fh))
	require.NoError(t, err)
	_, err = f.repo.ApplyImport(ctx, rootImport(f.projectID, snow.ID(2), gitOrigin("b"), "b.txt", ch, sz, fh))
	require.NoError(t, err)

	other := seedProject(t, sqlcSqlite.New(f.db), 1, "other-project")
	require.NoError(t, f.repo.LinkProjectChunk(ctx, other, ch))
	_, err = f.repo.ApplyImport(ctx, rootImport(other, snow.ID(3), gitOrigin("c"), "c.txt", ch, sz, fh))
	require.NoError(t, err)
	sameOrigin, err := f.repo.ApplyImport(ctx, rootImport(other, snow.ID(4), gitOrigin("a"), "a.txt", ch, sz, fh))
	require.NoError(t, err)
	require.Equal(t, snow.ID(4), sameOrigin.ID, "origins are unique per project only")

	commits, err := f.branches.CommitsByOrigins(ctx, f.projectID, []string{gitOrigin("a"), gitOrigin("b"), gitOrigin("c"), gitOrigin("z")})
	require.NoError(t, err)
	got := map[string]snow.ID{}
	for _, c := range commits {
		got[c.Origin] = c.ID
	}
	require.Equal(t, map[string]snow.ID{gitOrigin("a"): 1, gitOrigin("b"): 2}, got)

	none, err := f.branches.CommitsByOrigins(ctx, f.projectID, nil)
	require.NoError(t, err)
	require.Empty(t, none)

	many := make([]string, 0, originBatch+2)
	for i := 0; i < originBatch; i++ {
		many = append(many, gitOrigin("f"))
	}
	many = append(many, gitOrigin("b"), gitOrigin("a"))
	commits, err = f.branches.CommitsByOrigins(ctx, f.projectID, many)
	require.NoError(t, err)
	require.Len(t, commits, 2)
}
