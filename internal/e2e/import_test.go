package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

func importGitOrigin(n int) string { return fmt.Sprintf("git:default:%040x", n) }

// importFiles chunks contents like a push and uploads the chunks.
func importFiles(t *testing.T, c *clientgrpc.Client, contents map[string]string) []*serverDomain.PushFile {
	t.Helper()
	var files []*serverDomain.PushFile
	var chunks []*serverDomain.ChunkData
	for path, content := range contents {
		binary, err := chunker.ProbeBinary(bytes.NewReader([]byte(content)))
		require.NoError(t, err)
		encoding, encoded, err := chunker.EncodeBytes([]byte(content), path, binary, "")
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, 0, len(encoded))
		for _, e := range encoded {
			hashes = append(hashes, e.Hash)
			chunks = append(chunks, &serverDomain.ChunkData{Hash: e.Hash, Data: e.Data, RawSize: e.RawSize})
		}
		files = append(files, &serverDomain.PushFile{
			Path:        path,
			Mode:        0o644,
			SizeBytes:   int64(len(content)),
			IsBinary:    binary,
			Encoding:    encoding,
			FileHash:    treehash.FileHash(hashes),
			ChunkHashes: hashes,
		})
	}
	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: e2eOrgSlug, Project: e2eProjectSlug}, chunks)
	require.NoError(t, err)
	return files
}

func streamImport(t *testing.T, c *clientgrpc.Client, commits []*clientgrpc.ImportCommit) []*clientgrpc.ImportedCommit {
	t.Helper()
	session, err := c.OpenImport(context.Background(), e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	defer session.Close()
	for _, commit := range commits {
		require.NoError(t, session.Send(commit))
	}
	require.NoError(t, session.CloseSend())
	var got []*clientgrpc.ImportedCommit
	for {
		res, err := session.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		got = append(got, res)
	}
	return got
}

func TestEndToEnd_GitImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth, _ := newBenchClient(t, host)

	rootFiles := map[string]string{"a.txt": "hello\n", "art/logo.png": "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00\x01", 64)}
	childFiles := map[string]string{"a.txt": "hello, child\n"}
	mergeFiles := map[string]string{"docs/merge.md": "# merged\n"}
	authored := time.Date(2015, 6, 7, 8, 9, 10, 0, time.UTC)
	commits := []*clientgrpc.ImportCommit{
		{Origin: importGitOrigin(1), Message: "root", AuthorName: "Ann Author", AuthorEmail: "ann@example.com", AuthoredAt: authored, Files: importFiles(t, grpcClient, rootFiles)},
		{Origin: importGitOrigin(2), ParentOrigins: []string{importGitOrigin(1)}, Message: "child", AuthorName: "Bob", AuthorEmail: "bob@example.com", AuthoredAt: authored.Add(time.Hour), Files: importFiles(t, grpcClient, childFiles), Removed: []string{"art/logo.png"}},
		{Origin: importGitOrigin(3), ParentOrigins: []string{importGitOrigin(2), importGitOrigin(1)}, Message: "merge", AuthoredAt: authored.Add(2 * time.Hour), Files: importFiles(t, grpcClient, mergeFiles)},
	}

	imported := streamImport(t, grpcClient, commits)
	require.Len(t, imported, 3)
	for i, c := range imported {
		require.Equal(t, commits[i].Origin, c.Origin)
	}

	found, err := grpcClient.ImportLookup(ctx, e2eOrgSlug, e2eProjectSlug, []string{importGitOrigin(1), importGitOrigin(2), importGitOrigin(3), importGitOrigin(4)})
	require.NoError(t, err)
	require.Len(t, found, 3)
	require.Equal(t, imported[2].CommitID, found[importGitOrigin(3)].CommitID)

	results, err := grpcClient.ImportSetRefs(ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ImportRefs{
		Branches:      []clientgrpc.ImportRef{{Name: "main", Origin: importGitOrigin(3)}, {Name: "legacy", Origin: importGitOrigin(1)}},
		Tags:          []clientgrpc.ImportRef{{Name: "v1", Origin: importGitOrigin(2), Message: "first release"}},
		DefaultBranch: "main",
		CommitCount:   3,
	})
	require.NoError(t, err)
	statuses := map[string]string{}
	for _, r := range results {
		statuses[r.Kind+":"+r.Name] = r.Status
	}
	require.Equal(t, map[string]string{
		"branch:main":   serverusecase.ImportRefUpdated,
		"branch:legacy": serverusecase.ImportRefCreated,
		"tag:v1":        serverusecase.ImportRefCreated,
		"default:main":  serverusecase.ImportRefUnchanged,
	}, statuses)

	main := cloneWorktree(t, grpcClient, auth, host, "main")
	assertFileContent(t, main, "a.txt", childFiles["a.txt"])
	assertFileContent(t, main, "docs/merge.md", mergeFiles["docs/merge.md"])
	require.NoFileExists(t, main+"/art/logo.png")
	legacy := cloneWorktree(t, grpcClient, auth, host, "legacy")
	assertFileContent(t, legacy, "a.txt", rootFiles["a.txt"])
	assertFileContent(t, legacy, "art/logo.png", rootFiles["art/logo.png"])
	require.NoFileExists(t, legacy+"/docs/merge.md")

	again := streamImport(t, grpcClient, commits)
	require.Len(t, again, 3)
	for i := range again {
		require.Equal(t, imported[i].CommitID, again[i].CommitID, "a re-sent origin returns the stored commit")
		require.Equal(t, imported[i].CommitHash, again[i].CommitHash)
	}

	results, err = grpcClient.ImportSetRefs(ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ImportRefs{
		Branches: []clientgrpc.ImportRef{{Name: "legacy", Origin: importGitOrigin(3)}, {Name: "main", Origin: importGitOrigin(1)}},
		Update:   true,
	})
	require.NoError(t, err)
	require.Equal(t, serverusecase.ImportRefUpdated, results[0].Status, "legacy fast-forwards to the merge")
	require.Equal(t, serverusecase.ImportRefDiverged, results[1].Status, "main never moves backwards")

	repo := sqlite.NewBranchRepository(dbConn)
	root, err := repo.GetCommit(ctx, imported[0].CommitID)
	require.NoError(t, err)
	require.Equal(t, "Ann Author", root.AuthorName)
	require.Equal(t, "ann@example.com", root.AuthorEmail)
	require.Equal(t, importGitOrigin(1), root.Origin)
	require.True(t, authored.Equal(root.CreatedAt), "the author date is the commit date: %s", root.CreatedAt)
	require.Nil(t, root.Parent1ID)

	merge, err := repo.GetCommit(ctx, imported[2].CommitID)
	require.NoError(t, err)
	require.Equal(t, imported[1].CommitID, *merge.Parent1ID)
	require.Equal(t, imported[0].CommitID, *merge.Parent2ID)
	require.Empty(t, merge.AuthorName)

	detail, err := grpcClient.GetCommit(ctx, e2eOrgSlug, e2eProjectSlug, imported[1].CommitID.Base36())
	require.NoError(t, err)
	require.Equal(t, "child", detail.Message)
	require.Equal(t, imported[1].CommitHash.String(), detail.Hash)
	require.True(t, authored.Add(time.Hour).Equal(detail.CreatedAt), "GetCommit reports the author date: %s", detail.CreatedAt)
}

func TestEndToEnd_GitImportPreflight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	adminClient, _ := newLoggedInClient(t, host)

	checked, err := adminClient.ImportEstimate(ctx, e2eOrgSlug, e2eProjectSlug, 1<<40)
	require.NoError(t, err)
	require.False(t, checked, "the free server has no storage quota")
	found, err := adminClient.ImportLookup(ctx, e2eOrgSlug, e2eProjectSlug, nil)
	require.NoError(t, err)
	require.Empty(t, found)

	seedUserWithPassword(t, dbConn, 9200, "writer", "writer@example.com", "password123")
	_, err = adminClient.CreatePBACRule(ctx, e2eOrgSlug, e2eProjectSlug,
		snow.ID(9200).Base36(), "", "", uint64(serverDomain.PermissionRead|serverDomain.PermissionWrite))
	require.NoError(t, err)
	writer, _ := newClientWithPassword(t, host, "writer@example.com", "password123")

	_, err = writer.ImportLookup(ctx, e2eOrgSlug, e2eProjectSlug, nil)
	require.Equal(t, 403, clientErrorCode(t, err), "a project writer is refused before uploading anything")
	_, err = writer.ImportLookup(ctx, e2eOrgSlug, e2eProjectSlug, []string{importGitOrigin(1)})
	require.Equal(t, 403, clientErrorCode(t, err))
	_, err = writer.ImportEstimate(ctx, e2eOrgSlug, e2eProjectSlug, 10)
	require.Equal(t, 403, clientErrorCode(t, err))
}
