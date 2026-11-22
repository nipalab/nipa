package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

type TreeHashRepositorySuite struct {
	baseSuite
}

func TestTreeHashRepositorySuite(t *testing.T) {
	suite.Run(t, new(TreeHashRepositorySuite))
}

// legacyFormatTreeHash is the tree hash before file modes were part of it.
func legacyFormatTreeHash(files []treehash.FileEntry, trees []treehash.TreeEntry) domain.Hash {
	plain := make([]treehash.FileEntry, len(files))
	for i, f := range files {
		plain[i] = treehash.FileEntry{Name: f.Name, Hash: f.Hash}
	}
	return treehash.TreeHash(plain, trees)
}

// TestBackfill stores three commits the way a server
// without mode-aware hashes did: a read-write lib.so, a mode-only change to
// 0755 (same hash, so reads still returned the read-write rows) and a commit
// keeping lib/ by reference. The backfill gives every directory the hash of
// its own rows, so the chmod commit reads back as executable.
func (s *TreeHashRepositorySuite) TestBackfill() {
	t := s.T()
	ctx := context.Background()
	db := s.db
	projectID := seedProject(t, s.q, 1, "push-project")
	branchID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	seedUser(t, s.q, "pusher", "pusher@example.com", sql.NullString{})
	repo, branchRepo := NewPushRepository(db), NewBranchRepository(db)
	trees := NewTreeHashRepository(db)

	libChunk, libSize, libHash := chunkRow(t, "elf")
	aChunk, aSize, aHash := chunkRow(t, "a")
	bChunk, bSize, bHash := chunkRow(t, "b")
	for _, c := range []struct {
		hash domain.Hash
		size int64
	}{{libChunk, libSize}, {aChunk, aSize}, {bChunk, bSize}} {
		require.NoError(t, repo.InsertChunkIfNotExists(ctx, c.hash, c.size))
		require.NoError(t, repo.LinkProjectChunk(ctx, projectID, c.hash))
	}

	libRW := treehash.FileEntry{Name: "lib.so", Hash: libHash, Mode: 0o644}
	libX := treehash.FileEntry{Name: "lib.so", Hash: libHash, Mode: 0o755}
	fileA := treehash.FileEntry{Name: "a.txt", Hash: aHash, Mode: 0o644}
	fileB := treehash.FileEntry{Name: "a.txt", Hash: bHash, Mode: 0o644}
	oldLib := legacyFormatTreeHash([]treehash.FileEntry{libRW}, nil)
	oldRootA := legacyFormatTreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "lib", Hash: oldLib}})
	oldRootB := legacyFormatTreeHash([]treehash.FileEntry{fileB}, []treehash.TreeEntry{{Name: "lib", Hash: oldLib}})

	libRow := func(mode int) usecase.PushFileRow {
		return usecase.PushFileRow{Name: "lib.so", Mode: mode, SizeBytes: libSize, Hash: libHash, TreeID: 2, ChunkHashes: []domain.Hash{libChunk}}
	}
	aRow := usecase.PushFileRow{Name: "a.txt", Mode: 0o644, SizeBytes: aSize, Hash: aHash, TreeID: 1, ChunkHashes: []domain.Hash{aChunk}}
	bRow := usecase.PushFileRow{Name: "a.txt", Mode: 0o644, SizeBytes: bSize, Hash: bHash, TreeID: 1, ChunkHashes: []domain.Hash{bChunk}}
	fullCommit := func(id snow.ID, parent *snow.ID, libMode int) usecase.ApplyPushRequest {
		return usecase.ApplyPushRequest{
			ProjectID: projectID, BranchID: branchID, CommitID: id, ParentID: parent,
			CommitHash: treehash.CommitHash(oldRootA, nil, id.Base36()),
			UserID:     snow.ID(1), Message: id.Base36(),
			Nodes: []usecase.PushNodeRow{
				{ID: 1, Hash: oldRootA, Name: "root", Mode: 0o444},
				{ID: 2, Hash: oldLib, Name: "lib", Mode: 0o444, ParentID: ptr(int64(1))},
			},
			Files: []usecase.PushFileRow{aRow, libRow(libMode)},
		}
	}
	require.NoError(t, repo.ApplyPush(ctx, fullCommit(snow.ID(101), nil, 0o644)))
	require.NoError(t, repo.ApplyPush(ctx, fullCommit(snow.ID(102), ptr(snow.ID(101)), 0o755)))
	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(103), ParentID: ptr(snow.ID(102)),
		CommitHash: treehash.CommitHash(oldRootB, nil, "keep lib"),
		UserID:     snow.ID(1), Message: "keep lib",
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: oldRootB, Name: "root", Mode: 0o444},
			{ID: 2, Hash: oldLib, Name: "lib", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{bRow},
	}))

	libMode := func(commitID snow.ID) int {
		t.Helper()
		commit, err := branchRepo.GetCommit(ctx, commitID)
		require.NoError(t, err)
		lib, err := branchRepo.GetTreeChildByName(ctx, commit.TreeID, "lib")
		require.NoError(t, err)
		files, err := branchRepo.ListFilesByTree(ctx, lib.ID)
		require.NoError(t, err)
		require.Len(t, files, 1)
		return files[0].Mode
	}
	rootHash := func(commitID snow.ID) (int64, domain.Hash) {
		t.Helper()
		commit, err := branchRepo.GetCommit(ctx, commitID)
		require.NoError(t, err)
		root, err := branchRepo.GetTreeNode(ctx, commit.TreeID)
		require.NoError(t, err)
		return root.ID, root.Hash
	}
	require.Equal(t, 0o644, libMode(snow.ID(102)), "before the backfill the chmod commit reads the oldest rows")

	res, err := usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, 1, res.Seeds)
	require.Equal(t, 2, res.Updated, "only the chmod commit's lib/ and root change")

	newLibX := treehash.TreeHash([]treehash.FileEntry{libX}, nil)
	require.Equal(t, 0o755, libMode(snow.ID(102)))
	require.Equal(t, 0o644, libMode(snow.ID(101)))
	require.Equal(t, 0o644, libMode(snow.ID(103)), "the reference keeps the rows it resolved to")

	root101, hash101 := rootHash(snow.ID(101))
	root102, hash102 := rootHash(snow.ID(102))
	_, hash103 := rootHash(snow.ID(103))
	require.Equal(t, oldRootA, hash101)
	require.Equal(t, treehash.TreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "lib", Hash: newLibX}}), hash102)
	require.Equal(t, oldRootB, hash103)

	legacy, err := trees.LegacyTreeHash(ctx, root102)
	require.NoError(t, err)
	require.NotNil(t, legacy)
	require.Equal(t, oldRootA, *legacy)
	legacy, err = trees.LegacyTreeHash(ctx, root101)
	require.NoError(t, err)
	require.Nil(t, legacy)

	// The watermark is stored: a second pass only rescans the overlap window
	// (everything here is recent) and changes nothing ...
	res, err = usecase.BackfillTreeHashes(ctx, NewTreeHashRepository(db))
	require.NoError(t, err)
	require.Zero(t, res.FromID)
	require.Zero(t, res.Updated)
	// ... and once the rows are older than the window it has nothing to do.
	_, err = db.ExecContext(ctx, "UPDATE tree_nodes SET created_at = now() - interval '1 day'")
	require.NoError(t, err)
	res, err = usecase.BackfillTreeHashes(ctx, NewTreeHashRepository(db))
	require.NoError(t, err)
	require.Equal(t, res.FromID, res.UpToID)
	require.NotZero(t, res.FromID)

	// A full rerun is a no-op too.
	_, err = db.ExecContext(ctx, "UPDATE tree_hash_backfill SET watermark = 0 WHERE id = 1")
	require.NoError(t, err)
	res, err = usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, 1, res.Seeds)
	require.Zero(t, res.Updated)
	_, hash102again := rootHash(snow.ID(102))
	require.Equal(t, hash102, hash102again)
}

func (s *TreeHashRepositorySuite) TestEmptyDatabase() {
	t := s.T()
	ctx := context.Background()
	trees := NewTreeHashRepository(s.db)
	res, err := usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, usecase.TreeHashBackfillResult{}, res)
	legacy, err := trees.LegacyTreeHash(ctx, 42)
	require.NoError(t, err)
	require.Nil(t, legacy)
	_, err = trees.TreeHashNode(ctx, 42)
	require.True(t, domain.IsErrorNotFound(err))
}

// TestConcurrentPasses runs passes from several "pods" at once: the marker row
// lock serializes them, exactly one does the work and the rest see the new
// watermark.
func (s *TreeHashRepositorySuite) TestConcurrentPasses() {
	t := s.T()
	ctx := context.Background()
	tool := treehash.FileEntry{Name: "tool.sh", Hash: domain.Hash{0x01}, Mode: 0o755}
	var rootID int64
	require.NoError(t, s.db.QueryRowContext(ctx,
		"INSERT INTO tree_nodes (hash, name, mode) VALUES ($1, 'root', 292) RETURNING id",
		legacyFormatTreeHash([]treehash.FileEntry{tool}, nil).Bytes()).Scan(&rootID))
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO files (name, mode, tree_id, hash, size_bytes) VALUES ('tool.sh', 493, $1, $2, 0)",
		rootID, tool.Hash.Bytes())
	require.NoError(t, err)

	const pods = 4
	results := make(chan usecase.TreeHashBackfillResult, pods)
	errs := make(chan error, pods)
	for range pods {
		go func() {
			res, err := usecase.BackfillTreeHashes(ctx, NewTreeHashRepository(s.db))
			results <- res
			errs <- err
		}()
	}
	updated := 0
	for range pods {
		require.NoError(t, <-errs)
		updated += (<-results).Updated
	}
	require.Equal(t, 1, updated)
	var hash []byte
	require.NoError(t, s.db.QueryRowContext(ctx, "SELECT hash FROM tree_nodes WHERE id = $1", rootID).Scan(&hash))
	require.Equal(t, treehash.TreeHash([]treehash.FileEntry{tool}, nil).Bytes(), hash)
}

// TestRollingDeploy reproduces what an older server
// writes around a backfill pass: a push whose ids were taken before the pass
// read MAX(id) but that committed after it (old-format content rows below the
// watermark), and a push that kept lib/ by reference under the hash it read
// before the pass rewrote lib/ (a reference no content node resolves). The
// next pass rescans the overlap window and repoints the reference.
func (s *TreeHashRepositorySuite) TestRollingDeploy() {
	t := s.T()
	ctx := context.Background()
	db := s.db
	projectID := seedProject(t, s.q, 1, "push-project")
	branchID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	seedUser(t, s.q, "pusher", "pusher@example.com", sql.NullString{})
	repo, branchRepo := NewPushRepository(db), NewBranchRepository(db)
	trees := NewTreeHashRepository(db)

	libChunk, libSize, libHash := chunkRow(t, "elf")
	aChunk, aSize, aHash := chunkRow(t, "a")
	bChunk, bSize, bHash := chunkRow(t, "b")
	for _, c := range []struct {
		hash domain.Hash
		size int64
	}{{libChunk, libSize}, {aChunk, aSize}, {bChunk, bSize}} {
		require.NoError(t, repo.InsertChunkIfNotExists(ctx, c.hash, c.size))
		require.NoError(t, repo.LinkProjectChunk(ctx, projectID, c.hash))
	}
	libX := treehash.FileEntry{Name: "lib.so", Hash: libHash, Mode: 0o755}
	fileA := treehash.FileEntry{Name: "a.txt", Hash: aHash, Mode: 0o644}
	fileB := treehash.FileEntry{Name: "a.txt", Hash: bHash, Mode: 0o644}
	tool := treehash.FileEntry{Name: "tool.sh", Hash: aHash, Mode: 0o755}
	oldLib := legacyFormatTreeHash([]treehash.FileEntry{libX}, nil)
	oldRootA := legacyFormatTreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "lib", Hash: oldLib}})
	oldRootB := legacyFormatTreeHash([]treehash.FileEntry{fileB}, []treehash.TreeEntry{{Name: "lib", Hash: oldLib}})
	oldBin := legacyFormatTreeHash([]treehash.FileEntry{tool}, nil)
	oldRootLate := legacyFormatTreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}})
	newLib := treehash.TreeHash([]treehash.FileEntry{libX}, nil)
	newBin := treehash.TreeHash([]treehash.FileEntry{tool}, nil)

	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(101),
		CommitHash: treehash.CommitHash(oldRootA, nil, "first"),
		UserID:     snow.ID(1), Message: "first",
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: oldRootA, Name: "root", Mode: 0o444},
			{ID: 2, Hash: oldLib, Name: "lib", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "a.txt", Mode: 0o644, SizeBytes: aSize, Hash: aHash, TreeID: 1, ChunkHashes: []domain.Hash{aChunk}},
			{Name: "lib.so", Mode: 0o755, SizeBytes: libSize, Hash: libHash, TreeID: 2, ChunkHashes: []domain.Hash{libChunk}},
		},
	}))
	// The first commit is old: the overlap window starts after it.
	_, err := db.ExecContext(ctx, "UPDATE tree_nodes SET created_at = now() - interval '1 day'")
	require.NoError(t, err)
	var oldMax int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT MAX(id) FROM tree_nodes").Scan(&oldMax))

	// A push takes two ids, then a later one commits first.
	insertNode := func(hash domain.Hash, name string, parent any) int64 {
		t.Helper()
		var id int64
		require.NoError(t, db.QueryRowContext(ctx,
			"INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES ($1, $2, 292, $3) RETURNING id",
			hash.Bytes(), name, parent).Scan(&id))
		return id
	}
	lateRoot := insertNode(domain.Hash{0xaa}, "root", nil)
	lateBin := insertNode(domain.Hash{0xab}, "bin", lateRoot)
	_, err = db.ExecContext(ctx, "DELETE FROM tree_nodes WHERE id IN ($1, $2)", lateBin, lateRoot)
	require.NoError(t, err)
	pad := legacyFormatTreeHash([]treehash.FileEntry{fileB}, nil)
	padID := insertNode(pad, "root", nil)
	_, err = db.ExecContext(ctx, "INSERT INTO files (name, mode, tree_id, hash, size_bytes) VALUES ('a.txt', 420, $1, $2, 0)", padID, bHash.Bytes())
	require.NoError(t, err)

	res, err := usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, padID, res.UpToID)
	require.Equal(t, 2, res.Updated)

	// The late push commits below the watermark ...
	for _, row := range []struct {
		id     int64
		hash   domain.Hash
		name   string
		parent any
	}{{lateRoot, oldRootLate, "root", nil}, {lateBin, oldBin, "bin", lateRoot}} {
		_, err = db.ExecContext(ctx, "INSERT INTO tree_nodes (id, hash, name, mode, parent_tree_id) VALUES ($1, $2, $3, 292, $4)", row.id, row.hash.Bytes(), row.name, row.parent)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO files (name, mode, tree_id, hash, size_bytes) VALUES ('a.txt', 420, $1, $2, 0), ('tool.sh', 493, $3, $4, 0)",
		lateRoot, aHash.Bytes(), lateBin, aHash.Bytes())
	require.NoError(t, err)
	// ... and an older server keeps lib/ under the hash it read before the pass.
	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(102), ParentID: ptr(snow.ID(101)),
		CommitHash: treehash.CommitHash(oldRootB, nil, "keep lib"),
		UserID:     snow.ID(1), Message: "keep lib",
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: oldRootB, Name: "root", Mode: 0o444},
			{ID: 2, Hash: oldLib, Name: "lib", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{{Name: "a.txt", Mode: 0o644, SizeBytes: bSize, Hash: bHash, TreeID: 1, ChunkHashes: []domain.Hash{bChunk}}},
	}))
	commit, err := branchRepo.GetCommit(ctx, snow.ID(102))
	require.NoError(t, err)
	lib, err := branchRepo.GetTreeChildByName(ctx, commit.TreeID, "lib")
	require.NoError(t, err)
	files, err := branchRepo.ListFilesByTree(ctx, lib.ID)
	require.NoError(t, err)
	require.Empty(t, files, "the reference resolves to nothing before the repair")

	res, err = usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, oldMax, res.FromID, "only the overlap window is rescanned")
	require.Equal(t, 1, res.Seeds)
	require.Equal(t, 1, res.Repaired)
	require.Equal(t, 4, res.Updated)

	files, err = branchRepo.ListFilesByTree(ctx, lib.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "lib.so", files[0].Name)
	require.Equal(t, 0o755, files[0].Mode)
	hashOf := func(id int64) domain.Hash {
		t.Helper()
		n, err := branchRepo.GetTreeNode(ctx, id)
		require.NoError(t, err)
		return n.Hash
	}
	require.Equal(t, newLib, hashOf(lib.ID))
	require.Equal(t, treehash.TreeHash([]treehash.FileEntry{fileB}, []treehash.TreeEntry{{Name: "lib", Hash: newLib}}), hashOf(commit.TreeID))
	require.Equal(t, newBin, hashOf(lateBin))
	require.Equal(t, treehash.TreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "bin", Hash: newBin}}), hashOf(lateRoot))
	legacy, err := trees.LegacyTreeHash(ctx, commit.TreeID)
	require.NoError(t, err)
	require.Equal(t, oldRootB, *legacy)

	// Idempotent.
	res, err = usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Zero(t, res.Repaired)
	require.Zero(t, res.Updated)
}
