package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

var guardScope = domain.ChunkScope{Org: "o", Project: "p"}

func guardedSync(lr *localrepo.LocalRepo, root string, client chunkDownloader, tree *serverDomain.TreeNode) error {
	return syncWorkingCopyOpts(context.Background(), client, lr, root, tree, guardScope, syncOptions{keepLocalChanges: true})
}

func textManifestFile(t *testing.T, name, content string, data map[serverDomain.Hash][]byte) *serverDomain.File {
	t.Helper()
	hash, chunks, stored, encoding := testEncodedFile(t, name, content)
	for h, b := range stored {
		data[h] = b
	}
	return &serverDomain.File{Name: name, Mode: 2, SizeBytes: int64(len(content)), Encoding: encoding, Hash: hash, Chunks: chunks}
}

// syncedRepo returns a clone synced to a tree holding a.bin ("ONE") and
// notes.txt ("first\n"), with the downloader that served it.
func syncedRepo(t *testing.T) (*localrepo.LocalRepo, string, *recordingDownloader) {
	t.Helper()
	lr, root := newDirectRepo(t)
	client := &recordingDownloader{data: map[serverDomain.Hash][]byte{}}
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", client.data, "ONE"),
		textManifestFile(t, "notes.txt", "first\n", client.data),
	}}
	require.NoError(t, syncTree(t, lr, root, client, tree))
	client.requested = nil
	return lr, root, client
}

func nextTree(t *testing.T, client *recordingDownloader) *serverDomain.TreeNode {
	t.Helper()
	return &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", client.data, "TWO"),
		textManifestFile(t, "notes.txt", "second\n", client.data),
	}}
}

func TestGuardedSync_UnchangedWorkingCopyUpdates(t *testing.T) {
	lr, root, client := syncedRepo(t)

	require.NoError(t, guardedSync(lr, root, client, nextTree(t, client)))
	require.Equal(t, "TWO", string(readRepoFile(t, root, "a.bin")))
	require.Equal(t, "second\n", string(readRepoFile(t, root, "notes.txt")))
}

func TestGuardedSync_RefusesToOverwriteLocalEdits(t *testing.T) {
	lr, root, client := syncedRepo(t)
	writeRepoFile(t, root, "a.bin", "MINE")
	writeRepoFile(t, root, "notes.txt", "my notes\n")

	err := guardedSync(lr, root, client, nextTree(t, client))

	var userErr *domain.Error
	require.ErrorAs(t, err, &userErr)
	require.Equal(t, 400, userErr.Code)
	require.Contains(t, userErr.Message, "local changes to 2 file(s) would be overwritten")
	require.Contains(t, userErr.Message, "\n  a.bin\n  notes.txt")
	require.Contains(t, userErr.Action, "--force")
	require.Equal(t, "MINE", string(readRepoFile(t, root, "a.bin")))
	require.Equal(t, "my notes\n", string(readRepoFile(t, root, "notes.txt")))
	require.Empty(t, client.requested, "a refused sync downloads nothing")
	requireNoTempFiles(t, root)
}

func TestGuardedSync_OnlyFilesTheSyncReplacesCount(t *testing.T) {
	lr, root, client := syncedRepo(t)
	writeRepoFile(t, root, "notes.txt", "my notes\n")
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", client.data, "TWO"),
		textManifestFile(t, "notes.txt", "first\n", client.data),
	}}

	require.NoError(t, guardedSync(lr, root, client, tree), "an edit to a file the update leaves alone does not block it")
	require.Equal(t, "TWO", string(readRepoFile(t, root, "a.bin")))
	require.Equal(t, "my notes\n", string(readRepoFile(t, root, "notes.txt")))
}

func TestGuardedSync_LocalContentEqualToTargetIsNotLost(t *testing.T) {
	lr, root, client := syncedRepo(t)
	writeRepoFile(t, root, "a.bin", "TWO")
	writeRepoFile(t, root, "notes.txt", "second\n")

	require.NoError(t, guardedSync(lr, root, client, nextTree(t, client)))
	require.Equal(t, "TWO", string(readRepoFile(t, root, "a.bin")))
}

func TestGuardedSync_MissingWorkingFileIsRestored(t *testing.T) {
	lr, root, client := syncedRepo(t)
	require.NoError(t, os.Remove(filepath.Join(root, "a.bin")))

	require.NoError(t, guardedSync(lr, root, client, nextTree(t, client)))
	require.Equal(t, "TWO", string(readRepoFile(t, root, "a.bin")))
}

func TestGuardedSync_UntrackedFileInTheWay(t *testing.T) {
	lr, root, client := syncedRepo(t)
	tree := nextTree(t, client)
	tree.FileChildren = append(tree.FileChildren, rawManifestFile("new.bin", client.data, "FRESH"))

	writeRepoFile(t, root, "new.bin", "untracked work")
	err := guardedSync(lr, root, client, tree)
	require.ErrorContains(t, err, "\n  new.bin")
	require.Equal(t, "untracked work", string(readRepoFile(t, root, "new.bin")))
	require.Equal(t, "ONE", string(readRepoFile(t, root, "a.bin")), "nothing else was written either")

	writeRepoFile(t, root, "new.bin", "FRESH")
	require.NoError(t, guardedSync(lr, root, client, tree), "an untracked file that already has the incoming content is not lost")
}

func TestGuardedSync_DirectoryInTheWay(t *testing.T) {
	lr, root, client := syncedRepo(t)
	tree := nextTree(t, client)
	tree.FileChildren = append(tree.FileChildren, rawManifestFile("new.bin", client.data, "FRESH"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "new.bin", "sub"), 0o755))
	writeRepoFile(t, root, "new.bin/sub/mine", "mine")

	// A folder with content where a file goes is refused before anything is
	// written, and it is not reported as a local change of a tracked file.
	err := guardedSync(lr, root, client, tree)
	require.ErrorContains(t, err, "new.bin/sub/mine")
	require.NotContains(t, err.Error(), "local changes")
	require.Equal(t, "ONE", string(readRepoFile(t, root, "a.bin")), "nothing else was written either")

	// An empty folder holds nothing to lose: it gives way to the file.
	require.NoError(t, os.Remove(filepath.Join(root, "new.bin", "sub", "mine")))
	require.NoError(t, guardedSync(lr, root, client, tree))
	require.Equal(t, "FRESH", string(readRepoFile(t, root, "new.bin")))
}

func TestGuardedSync_UnreadableFileCountsAsLocalChange(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	lr, root, client := syncedRepo(t)
	writeRepoFile(t, root, "a.bin", "MINE")
	require.NoError(t, os.Chmod(filepath.Join(root, "a.bin"), 0))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "a.bin"), 0o644) })

	require.ErrorContains(t, guardedSync(lr, root, client, nextTree(t, client)), "\n  a.bin")
}

func TestLocalChangeConflicts_TrustsFreshStatFingerprint(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.bin", "whatever is on disk")
	info, err := os.Stat(filepath.Join(root, "a.bin"))
	require.NoError(t, err)
	base, target, other := serverDomain.Hash{1}, serverDomain.Hash{2}, serverDomain.Hash{3}
	candidate := overwriteCandidate{
		target:  materializedFile{Path: "a.bin", FileHash: target},
		base:    domain.SnapshotFile{Path: "a.bin", Hash: base},
		tracked: true,
	}
	for name, tc := range map[string]struct {
		hash     serverDomain.Hash
		conflict bool
	}{
		"fingerprint of the synced version":   {hash: base},
		"fingerprint of the incoming version": {hash: target},
		"fingerprint of other content":        {hash: other, conflict: true},
	} {
		t.Run(name, func(t *testing.T) {
			lr := &stubLocalRepo{statCache: map[string]domain.StatEntry{"a.bin": statEntryFromInfo(info, tc.hash)}}
			conflicts, err := localChangeConflicts(context.Background(), root, lr, []overwriteCandidate{candidate})
			require.NoError(t, err)
			require.Equal(t, tc.conflict, len(conflicts) == 1, "the fingerprint decides without reading the file")
		})
	}

	t.Run("incoming hash under another encoding proves nothing", func(t *testing.T) {
		c := candidate
		c.base.Encoding, c.target.Encoding = "raw", "zstd"
		lr := &stubLocalRepo{statCache: map[string]domain.StatEntry{"a.bin": statEntryFromInfo(info, target)}}
		conflicts, err := localChangeConflicts(context.Background(), root, lr, []overwriteCandidate{c})
		require.NoError(t, err)
		require.Equal(t, []string{"a.bin"}, conflicts)
	})
}

func TestLocalChangeConflicts_Errors(t *testing.T) {
	root := t.TempDir()
	candidates := []overwriteCandidate{{target: materializedFile{Path: "a.bin"}}}

	wantErr := errors.New("stat cache failed")
	_, err := localChangeConflicts(context.Background(), root, &stubLocalRepo{statCacheErr: wantErr}, candidates)
	require.ErrorIs(t, err, wantErr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = localChangeConflicts(ctx, root, struct{}{}, candidates)
	require.ErrorIs(t, err, context.Canceled)

	conflicts, err := localChangeConflicts(context.Background(), root, struct{}{}, nil)
	require.NoError(t, err)
	require.Empty(t, conflicts)
}

func TestLocalChangesError_CapsTheList(t *testing.T) {
	var paths []string
	for i := range maxListedLocalChanges + 5 {
		paths = append(paths, fmt.Sprintf("file%02d.bin", i))
	}
	msg := localChangesError(paths).Error()
	require.Contains(t, msg, "local changes to 25 file(s)")
	require.Contains(t, msg, "\n  file19.bin\n  ... and 5 more")
	require.NotContains(t, msg, "file20.bin")
}

// changedUpstream is an Update over a clone that tracks a.txt ("synced") and
// has local edits to it, while the server now has other content for it.
func changedUpstream(t *testing.T) (*Update, *stubLocalRepo, *stubUpdateClient, string) {
	t.Helper()
	root := t.TempDir()
	baseHash, _, _, _ := testEncodedFile(t, "a.txt", "synced")
	newHash, chunks, stored, encoding := testEncodedFile(t, "a.txt", "from the server")
	writeRepoFile(t, root, "a.txt", "my edits")
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "a.txt", Hash: baseHash, Mode: 2, Encoding: encoding}}},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
		loadCommit:    &domain.LocalCommit{CommitID: "c1"},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{{
			Name: "a.txt", Mode: 2, SizeBytes: int64(len("from the server")), Encoding: encoding, Hash: newHash, Chunks: chunks,
		}}},
		downloadData: stored,
		tagInfo:      &domain.Tag{Name: "v1", CommitID: "c1"},
	}
	client.commitManifest = client.manifest
	return newTestUpdate(t, local, client), local, client, root
}

func TestUpdate_RefusesToOverwriteLocalEditsUnlessForced(t *testing.T) {
	type run func(u *Update, root string) error
	for name, sync := range map[string]run{
		"update":     func(u *Update, root string) error { return u.Run(context.Background(), root) },
		"switch":     func(u *Update, root string) error { return u.Switch(context.Background(), root, "dev") },
		"switch tag": func(u *Update, root string) error { return u.SwitchTag(context.Background(), root, "v1") },
	} {
		t.Run(name, func(t *testing.T) {
			updater, local, client, root := changedUpstream(t)

			err := sync(updater, root)
			require.ErrorContains(t, err, "local changes to 1 file(s) would be overwritten")
			require.Equal(t, "my edits", string(readRepoFile(t, root, "a.txt")))
			require.Empty(t, client.downloadHashes)
			require.Nil(t, local.tree, "a refused sync does not move the snapshot")

			require.NoError(t, sync(updater.WithForce(true), root))
			require.Equal(t, "from the server", string(readRepoFile(t, root, "a.txt")))
			require.NotNil(t, local.tree)
			require.False(t, updater.force, "WithForce leaves the original Update alone")
		})
	}
}

func TestUpdate_DetachedRefusesToOverwriteLocalEdits(t *testing.T) {
	updater, local, _, root := changedUpstream(t)
	local.loadConfig.Head = &domain.HeadRef{Kind: domain.HeadKindTag, Name: "v1"}

	require.ErrorContains(t, updater.Run(context.Background(), root), "local changes")
	require.Equal(t, "my edits", string(readRepoFile(t, root, "a.txt")))
	require.NoError(t, updater.WithForce(true).Run(context.Background(), root))
	require.Equal(t, "from the server", string(readRepoFile(t, root, "a.txt")))
}
