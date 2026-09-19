package usecase

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubRevertClient struct {
	connectErr  error
	details     map[string]*domain.CommitDetail
	detailErr   error
	walkEntries []*domain.CommitWalkEntry
	walkErr     error
	walkStart   string
	walkStop    string
	walkLimit   int
	headTree    *serverDomain.TreeNode
	headErr     error
	chunks      map[serverDomain.Hash][]byte
	downloadErr error
	downloaded  []serverDomain.Hash
}

func (s *stubRevertClient) Connect(_ context.Context, _ string) error {
	return s.connectErr
}

func (s *stubRevertClient) GetCommit(_ context.Context, _, _, commitID string) (*domain.CommitDetail, error) {
	if s.detailErr != nil {
		return nil, s.detailErr
	}
	detail, ok := s.details[commitID]
	if !ok {
		return nil, domain.NewUserError(fmt.Sprintf("commit %s not found", commitID))
	}
	return detail, nil
}

func (s *stubRevertClient) WalkCommits(_ context.Context, _, _, start, stop string, limit int) ([]*domain.CommitWalkEntry, error) {
	s.walkStart, s.walkStop, s.walkLimit = start, stop, limit
	return s.walkEntries, s.walkErr
}

func (s *stubRevertClient) GetTreeNodeManifest(_ context.Context, _, _, _, _ string) (*serverDomain.TreeNode, error) {
	return s.headTree, s.headErr
}

func (s *stubRevertClient) DownloadChunks(_ context.Context, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	s.downloaded = append(s.downloaded, hashes...)
	if s.downloadErr != nil {
		return s.downloadErr
	}
	for _, h := range hashes {
		data, ok := s.chunks[h]
		if !ok {
			return fmt.Errorf("chunk %s not available", h)
		}
		if err := onChunk(h, data); err != nil {
			return err
		}
	}
	return nil
}

func newTestRevert(t *testing.T, client *stubRevertClient, local *stubLocalRepo) (*Revert, *stubPushClient) {
	t.Helper()
	pushClient := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	push := newTestPush(t, local, pushClient)
	return NewRevert(push.auth, client, local, push), pushClient
}

func revertConfig() *domain.Config {
	return &domain.Config{Url: "http://example.com/org/project", Branch: "main"}
}

type testBlob struct {
	file   merge.File
	chunks []*serverDomain.ChunkData
}

func testFile(t *testing.T, path, content string) testBlob {
	t.Helper()
	var hashes []serverDomain.Hash
	var sizes []int64
	data := make([]*serverDomain.ChunkData, 0, 1)
	err := chunker.Scan(bytes.NewReader([]byte(content)), func(c chunker.Chunk) error {
		hashes = append(hashes, c.Hash)
		sizes = append(sizes, int64(len(c.Data)))
		data = append(data, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		return nil
	})
	require.NoError(t, err)
	return testBlob{
		file: merge.File{
			Path:        path,
			Mode:        0o644,
			SizeBytes:   int64(len(content)),
			IsBinary:    chunker.IsBinary([]byte(content)),
			Hash:        chunker.FileHash(hashes),
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		},
		chunks: data,
	}
}

func testTree(hashByte byte, files ...merge.File) *serverDomain.TreeNode {
	byPath := make(map[string]merge.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	tree := treeFromFiles(byPath)
	tree.Hash = serverDomain.Hash{hashByte}
	return tree
}

func storeBlobs(local *stubLocalRepo, blobs ...testBlob) {
	if local.storedChunks == nil {
		local.storedChunks = make(map[serverDomain.Hash][]byte)
	}
	for _, b := range blobs {
		for _, c := range b.chunks {
			local.storedChunks[c.Hash] = c.Data
		}
	}
}

func snapshotOf(tree *serverDomain.TreeNode) *domain.Snapshot {
	snapshot := &domain.Snapshot{TreeHash: tree.Hash.String()}
	for _, f := range merge.Flatten(tree) {
		snapshot.Files = append(snapshot.Files, domain.SnapshotFile{
			Path:      f.Path,
			Hash:      f.Hash,
			Mode:      f.Mode,
			SizeBytes: f.SizeBytes,
			IsBinary:  f.IsBinary,
		})
	}
	return snapshot
}

func TestRevert_SingleCommit_Clean(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v2")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	head := testTree(0xaa, v2.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "2", RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Empty(t, outcome.Conflicts)
	require.False(t, outcome.NoChange)

	require.Len(t, pushClient.pushes, 1)
	require.Equal(t, head.Hash.String(), pushClient.pushes[0].baseTreeHash)
	require.Equal(t, "Revert \"change a\"\n\nThis reverts commit 2 (c2hash).", pushClient.pushes[0].message)
	require.Empty(t, pushClient.pushes[0].parent2)
	require.True(t, local.clearedRevert)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(data))
}

func TestRevert_SingleCommit_ConflictAndContinue(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "top\nv3\nbottom")
	v1 := testFile(t, "a.txt", "top\nv1\nbottom")
	v2 := testFile(t, "a.txt", "top\nv2\nbottom")
	v3 := testFile(t, "a.txt", "top\nv3\nbottom")
	head := testTree(0xaa, v3.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1, v2, v3)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "2", RevertOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)
	require.Empty(t, pushClient.pushes)
	require.False(t, local.clearedRevert)
	require.NotNil(t, local.savedRevert)
	require.Equal(t, head.Hash.String(), local.savedRevert.CurrentTreeHash)

	writeRepoFile(t, root, "a.txt", "resolved")
	require.NoError(t, local.StageAdd("a.txt"))

	outcome, err = revert.Run(context.Background(), root, "", RevertOptions{Continue: true})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.True(t, local.clearedRevert)
	require.Len(t, pushClient.pushes, 1)
	require.Equal(t, head.Hash.String(), pushClient.pushes[0].baseTreeHash)
	require.Equal(t, "Revert \"change a\"\n\nThis reverts commit 2 (c2hash).", pushClient.pushes[0].message)
}

func TestRevert_SingleCommit_Abort(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v2")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	head := testTree(0xaa, v2.file)

	client := &stubRevertClient{headTree: head}
	local := &stubLocalRepo{
		loadConfig: revertConfig(),
		snapshot:   snapshotOf(head),
		revertState: &domain.RevertState{
			Targets:            []domain.CommitRef{{ID: "2", Hash: "c2hash", Subject: "change a"}},
			CurrentTreeHash:    head.Hash.String(),
			OriginalTreeHash:   head.Hash.String(),
			OriginalCommitID:   "2",
			OriginalCommitHash: "c2hash",
		},
	}
	storeBlobs(local, v1, v2)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "", RevertOptions{Abort: true})
	require.NoError(t, err)
	require.False(t, outcome.Committed)
	require.Empty(t, pushClient.pushes)
	require.True(t, local.clearedRevert)
	require.True(t, local.clearedStaged)
	require.Equal(t, "2", local.savedCommitID)
	require.Equal(t, "c2hash", local.savedCommitHash)
}

func TestRevert_SingleCommit_NoCommit(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v2")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	head := testTree(0xaa, v2.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "2", RevertOptions{NoCommit: true})
	require.NoError(t, err)
	require.False(t, outcome.Committed)
	require.Empty(t, pushClient.pushes)
	require.True(t, local.clearedRevert)
	require.NotNil(t, local.tree)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(data))
}

func TestRevert_SingleCommit_NoChange(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v1")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	head := testTree(0xaa, v1.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "2", RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.NoChange)
	require.Empty(t, pushClient.pushes)
	require.True(t, local.clearedRevert)
}

func TestRevert_RootCommit_DeletesFiles(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v1")
	v1 := testFile(t, "a.txt", "v1")
	head := testTree(0xaa, v1.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "1", RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Len(t, pushClient.pushes, 1)
	require.Equal(t, []string{"a.txt"}, pushClient.pushes[0].removed)
	require.False(t, fileExists(root, "a.txt"))
}

func TestRevert_MergeCommit_RequiresMainline(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "ours")
	ours := testFile(t, "a.txt", "ours")
	p1 := testFile(t, "a.txt", "p1")
	p2 := testFile(t, "a.txt", "p2")
	head := testTree(0xaa, ours.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"5": {ID: "5", Hash: "m5", Parent1ID: "3", Parent2ID: "4", Message: "merge", Tree: testTree(0x05, ours.file)},
			"3": {ID: "3", Hash: "c3", Message: "on main", Tree: testTree(0x03, p1.file)},
			"4": {ID: "4", Hash: "c4", Message: "side", Tree: testTree(0x04, p2.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, ours, p1, p2)

	revert, _ := newTestRevert(t, client, local)
	_, err := revert.Run(context.Background(), root, "5", RevertOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--mainline")

	outcome, err := revert.Run(context.Background(), root, "5", RevertOptions{Mainline: 2})
	require.NoError(t, err)
	require.True(t, outcome.Committed)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "p2", string(data))
}

func TestRevert_Range_Clean(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v3")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	v3 := testFile(t, "a.txt", "v3")
	head := testTree(0xaa, v3.file)

	client := &stubRevertClient{
		walkEntries: []*domain.CommitWalkEntry{
			{ID: "3", Hash: "h3", Parent1ID: "2", Message: "third"},
			{ID: "2", Hash: "h2", Parent1ID: "1", Message: "second"},
		},
		details: map[string]*domain.CommitDetail{
			"3": {ID: "3", Hash: "h3", Parent1ID: "2", Message: "third", Tree: testTree(0x03, v3.file)},
			"2": {ID: "2", Hash: "h2", Parent1ID: "1", Message: "second", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "h1", Message: "first", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1, v2, v3)

	revert, pushClient := newTestRevert(t, client, local)
	pushClient.pushResults = []*serverDomain.PushResult{
		{CommitID: 10, CommitHash: serverDomain.Hash{0x10}, TreeHash: serverDomain.Hash{0x11}},
		{CommitID: 11, CommitHash: serverDomain.Hash{0x12}, TreeHash: serverDomain.Hash{0x13}},
	}

	outcome, err := revert.Run(context.Background(), root, "1..3", RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.True(t, local.clearedRevert)

	require.Equal(t, "3", client.walkStart)
	require.Equal(t, "1", client.walkStop)
	require.Len(t, pushClient.pushes, 2)
	require.Equal(t, head.Hash.String(), pushClient.pushes[0].baseTreeHash)
	require.Equal(t, "Revert \"third\"\n\nThis reverts commit 3 (h3).", pushClient.pushes[0].message)
	require.Equal(t, serverDomain.Hash{0x11}.String(), pushClient.pushes[1].baseTreeHash)
	require.Equal(t, "Revert \"second\"\n\nThis reverts commit 2 (h2).", pushClient.pushes[1].message)
	require.Empty(t, pushClient.pushes[1].parent2)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(data))
}

func TestRevert_Range_NoCommit(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v3")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	v3 := testFile(t, "a.txt", "v3")
	head := testTree(0xaa, v3.file)

	client := &stubRevertClient{
		walkEntries: []*domain.CommitWalkEntry{
			{ID: "3", Hash: "h3", Parent1ID: "2", Message: "third"},
			{ID: "2", Hash: "h2", Parent1ID: "1", Message: "second"},
		},
		details: map[string]*domain.CommitDetail{
			"3": {ID: "3", Hash: "h3", Parent1ID: "2", Message: "third", Tree: testTree(0x03, v3.file)},
			"2": {ID: "2", Hash: "h2", Parent1ID: "1", Message: "second", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "h1", Message: "first", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1, v2, v3)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "1..3", RevertOptions{NoCommit: true})
	require.NoError(t, err)
	require.False(t, outcome.Committed)
	require.Empty(t, pushClient.pushes)
	require.True(t, local.clearedRevert)
	require.NotNil(t, local.tree)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(data))
}

func TestRevert_Range_NoCommit_ConflictAndContinue(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "top\nv3\nbottom")
	v0 := testFile(t, "a.txt", "top\nv0\nbottom")
	v1 := testFile(t, "a.txt", "top\nv1\nbottom")
	v2 := testFile(t, "a.txt", "top\nv2\nbottom")
	v3 := testFile(t, "a.txt", "top\nv3\nbottom")
	head := testTree(0xaa, v3.file)

	client := &stubRevertClient{
		walkEntries: []*domain.CommitWalkEntry{
			{ID: "2", Hash: "h2", Parent1ID: "1", Message: "second"},
			{ID: "1", Hash: "h1", Parent1ID: "0", Message: "first"},
		},
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "h2", Parent1ID: "1", Message: "second", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "h1", Parent1ID: "0", Message: "first", Tree: testTree(0x01, v1.file)},
			"0": {ID: "0", Hash: "h0", Message: "root", Tree: testTree(0x00, v0.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v0, v1, v2, v3)

	revert, pushClient := newTestRevert(t, client, local)
	outcome, err := revert.Run(context.Background(), root, "0..2", RevertOptions{NoCommit: true})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)
	require.Empty(t, pushClient.pushes)
	require.False(t, local.clearedRevert)

	writeRepoFile(t, root, "a.txt", "top\nv1\nbottom")
	require.NoError(t, local.StageAdd("a.txt"))

	outcome, err = revert.Run(context.Background(), root, "", RevertOptions{Continue: true})
	require.NoError(t, err)
	require.Empty(t, outcome.Conflicts)
	require.True(t, local.clearedRevert)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "top\nv0\nbottom", string(data))
}

func TestRevert_Skip(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "top\nv3\nbottom")
	v1 := testFile(t, "a.txt", "top\nv1\nbottom")
	v2 := testFile(t, "a.txt", "top\nv2\nbottom")
	v3 := testFile(t, "a.txt", "top\nv3\nbottom")
	head := testTree(0xaa, v3.file)

	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head), storedChunks: map[serverDomain.Hash][]byte{}}
	storeBlobs(local, v1, v2, v3)

	revert, pushClient := newTestRevert(t, client, local)
	_, err := revert.Run(context.Background(), root, "2", RevertOptions{})
	require.NoError(t, err)
	require.False(t, local.clearedRevert)

	outcome, err := revert.Run(context.Background(), root, "", RevertOptions{Skip: true})
	require.NoError(t, err)
	require.False(t, outcome.Committed)
	require.Empty(t, pushClient.pushes)
	require.True(t, local.clearedRevert)
	require.True(t, local.clearedStaged)
}

func TestRevert_Guards(t *testing.T) {
	newLocal := func() *stubLocalRepo {
		return &stubLocalRepo{loadConfig: revertConfig(), snapshot: &domain.Snapshot{}}
	}
	client := &stubRevertClient{}

	t.Run("staged changes", func(t *testing.T) {
		local := newLocal()
		local.staged = []string{"a.txt"}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "staged changes")
	})

	t.Run("pending revert", func(t *testing.T) {
		local := newLocal()
		local.revertState = &domain.RevertState{Targets: []domain.CommitRef{{ID: "1"}}}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "already in progress")
	})

	t.Run("pending merge", func(t *testing.T) {
		local := newLocal()
		local.mergeState = &domain.MergeState{SourceBranch: "feature"}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "merge is already in progress")
	})

	t.Run("subdirectory clone", func(t *testing.T) {
		local := newLocal()
		local.loadConfig = &domain.Config{Url: "http://example.com/org/project/src", Branch: "main"}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "subdirectory")
	})

	t.Run("missing target", func(t *testing.T) {
		local := newLocal()
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "", RevertOptions{})
		require.ErrorContains(t, err, "commit to revert is required")
	})

	t.Run("stale head", func(t *testing.T) {
		local := newLocal()
		local.snapshot = &domain.Snapshot{TreeHash: "old"}
		head := testTree(0xaa)
		revert, _ := newTestRevert(t, &stubRevertClient{
			headTree: head,
			details: map[string]*domain.CommitDetail{
				"2": {ID: "2", Hash: "h2", Message: "change", Tree: testTree(0x02)},
			},
		}, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "nipa update")
	})

	t.Run("message with range", func(t *testing.T) {
		local := newLocal()
		head := testTree(0xaa)
		local.snapshot = snapshotOf(head)
		revert, _ := newTestRevert(t, &stubRevertClient{
			headTree:    head,
			walkEntries: []*domain.CommitWalkEntry{{ID: "2", Hash: "h2"}, {ID: "1", Hash: "h1"}},
		}, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "0..2", RevertOptions{Message: "custom"})
		require.ErrorContains(t, err, "--message")
	})

	t.Run("skip no-commit", func(t *testing.T) {
		local := newLocal()
		local.revertState = &domain.RevertState{
			Targets:  []domain.CommitRef{{ID: "2"}},
			NoCommit: true,
		}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(context.Background(), t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "--no-commit")
	})

	t.Run("continue without state", func(t *testing.T) {
		revert, _ := newTestRevert(t, client, newLocal())
		_, err := revert.Run(context.Background(), t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "no revert in progress")
	})

	t.Run("abort without state", func(t *testing.T) {
		revert, _ := newTestRevert(t, client, newLocal())
		_, err := revert.Run(context.Background(), t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "no revert in progress")
	})
}
