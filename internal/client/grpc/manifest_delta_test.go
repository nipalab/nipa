package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/treedelta"
)

func unchangedDir(depth int32, name string) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{Depth: depth, Name: name, Unchanged: true}}}
}

// deltaDir is a changed directory of a delta: no hash.
func deltaDir(depth int32, name string) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{Depth: depth, Name: name}}}
}

func unchangedFile(name string) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_File{File: &pb.TreeManifestFile{Name: name, Unchanged: true}}}
}

func TestClient_GetTreeManifestDelta_DecodesADelta(t *testing.T) {
	root := deltaDir(0, "root")
	root.GetDir().TreeHash = h32(7)
	fs := &fakeServer{rawManifestParts: []*pb.TreeManifestPart{
		{Branch: "main", View: []byte("view-1"), Delta: true, TargetCommitId: "tgt", Entries: []*pb.TreeManifestEntry{
			root, unchangedFile("keep.txt"), sizedFileEntry("big.bin", []int64{5}, h32(1)),
		}},
		{Entries: []*pb.TreeManifestEntry{
			chunksEntry([]int64{6, 7}, h32(2), h32(3)),
			unchangedFile("also.txt"),
			unchangedDir(1, "assets"),
			deltaDir(1, "src"), fileEntry("main.go", h32(4)),
			deltaDir(2, "lib"), unchangedFile("x.go"),
			unchangedDir(2, "vendor"),
			deltaDir(1, "zzz"),
		}},
	}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	baseHash := serverDomain.Hash{9}
	got, err := c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "main"}, []string{"a", "b"},
		treedelta.Base{CommitID: "base", View: []byte("view-1"), TreeHash: &baseHash})
	require.NoError(t, err)

	req := fs.lastManifestReq
	require.Equal(t, "main", req.GetBranch())
	require.Empty(t, req.GetCommitId())
	require.True(t, req.GetRecursive())
	require.Equal(t, []string{"a", "b"}, req.GetPaths())
	require.Equal(t, "base", req.GetBaseCommitId())
	require.Equal(t, []byte("view-1"), req.GetBaseView())
	require.Equal(t, baseHash.Bytes(), req.GetBaseTreeHash())

	require.Nil(t, got.Full)
	require.Equal(t, []byte("view-1"), got.View)
	require.Equal(t, "tgt", got.CommitID)
	var rootHash serverDomain.Hash
	copy(rootHash[:], h32(7))
	require.Equal(t, &rootHash, got.Delta.RootHash)

	d := got.Delta.Root
	require.Equal(t, "root", d.Name)
	require.False(t, d.Unchanged)
	require.Len(t, d.Files, 3)
	require.True(t, d.Files[0].Unchanged())
	require.Equal(t, "keep.txt", d.Files[0].Name)
	big := d.Files[1].Changed
	require.Equal(t, "big.bin", big.Name)
	require.Len(t, big.Chunks, 3)
	require.Equal(t, int64(7), big.Chunks[2].SizeBytes)
	require.NotZero(t, big.Hash, "a changed file is sealed like a full manifest's")
	require.True(t, d.Files[2].Unchanged())
	require.Equal(t, []*treedelta.Dir{
		{Name: "assets", Unchanged: true},
		{Name: "src",
			Files: []treedelta.File{{Name: "main.go", Changed: d.Dirs[1].Files[0].Changed}},
			Dirs: []*treedelta.Dir{
				{Name: "lib", Files: []treedelta.File{{Name: "x.go"}}},
				{Name: "vendor", Unchanged: true},
			}},
		{Name: "zzz"},
	}, d.Dirs)
	require.NotZero(t, d.Dirs[1].Files[0].Changed.Hash)

	// An unchanged root is a whole delta, with no root hash unless sent.
	fs.rawManifestParts = []*pb.TreeManifestPart{{Delta: true, View: []byte("v"), TargetCommitId: "same", Entries: []*pb.TreeManifestEntry{unchangedDir(0, "")}}}
	got, err = c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{CommitID: "same"}, nil, treedelta.Base{CommitID: "same", View: []byte("v")})
	require.NoError(t, err)
	require.Equal(t, &treedelta.Delta{Root: &treedelta.Dir{Unchanged: true}}, got.Delta)
	require.Equal(t, "same", fs.lastManifestReq.GetCommitId())
	require.Empty(t, fs.lastManifestReq.GetBaseTreeHash())
}

func TestClient_GetTreeManifestDelta_FullManifests(t *testing.T) {
	full := []*pb.TreeManifestPart{{Branch: "main", View: []byte("view-2"), Entries: []*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("a.txt", h32(1))}}}
	fs := &fakeServer{rawManifestParts: full}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	ctx := context.Background()

	// No base named: a plain manifest, and the token to keep with it.
	got, err := c.GetTreeManifestDelta(ctx, "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{})
	require.NoError(t, err)
	require.Nil(t, got.Delta)
	require.Empty(t, got.CommitID)
	require.Equal(t, []byte("view-2"), got.View)
	plain, err := c.GetTreeNodeManifest(ctx, "org", "project", "main", nil)
	require.NoError(t, err)
	require.Equal(t, plain, got.Full)
	require.Nil(t, fs.lastManifestReq.BaseCommitId)
	// A view without a base is not sent: it would mean nothing.
	_, err = c.GetTreeManifestDelta(ctx, "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{View: []byte("stale")})
	require.NoError(t, err)
	require.Empty(t, fs.lastManifestReq.GetBaseView())

	// The server declined: the same full manifest.
	got, err = c.GetTreeManifestDelta(ctx, "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{CommitID: "base", View: []byte("old")})
	require.NoError(t, err)
	require.Nil(t, got.Delta)
	require.Equal(t, plain, got.Full)

	// A server that predates deltas: no token, a full manifest.
	fs.rawManifestParts = []*pb.TreeManifestPart{{Branch: "main", Entries: full[0].Entries}}
	got, err = c.GetTreeManifestDelta(ctx, "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{CommitID: "base", View: []byte("old")})
	require.NoError(t, err)
	require.Empty(t, got.View)
	require.Equal(t, plain, got.Full)

	// No tree at all.
	fs.rawManifestParts = []*pb.TreeManifestPart{{Branch: "main", View: []byte("v")}}
	got, err = c.GetTreeManifestDelta(ctx, "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{})
	require.NoError(t, err)
	require.Nil(t, got.Full)
	require.Nil(t, got.Delta)
	require.Equal(t, []byte("v"), got.View)
}

func TestClient_GetTreeManifestDelta_RejectsMalformedDeltas(t *testing.T) {
	cases := map[string][]*pb.TreeManifestEntry{
		"no root":                         nil,
		"file before any directory":       {unchangedFile("a")},
		"chunks before any file":          {deltaDir(0, ""), chunksEntry(nil, h32(1))},
		"chunks after an unchanged file":  {deltaDir(0, ""), unchangedFile("a"), chunksEntry(nil, h32(1))},
		"unchanged file with chunks":      {deltaDir(0, ""), {Entry: &pb.TreeManifestEntry_File{File: &pb.TreeManifestFile{Name: "a", Unchanged: true, ChunkHashes: [][]byte{h32(1)}}}}},
		"file in an unchanged directory":  {deltaDir(0, ""), unchangedDir(1, "a"), unchangedFile("x")},
		"dir in an unchanged directory":   {deltaDir(0, ""), unchangedDir(1, "a"), deltaDir(2, "b")},
		"entries under an unchanged root": {unchangedDir(0, ""), unchangedDir(1, "a")},
		"second root":                     {deltaDir(0, ""), deltaDir(0, "again")},
		"depth skips a level":             {deltaDir(0, ""), deltaDir(2, "deep")},
		"short root hash":                 {{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{TreeHash: []byte{1, 2}}}}},
		"short chunk hash":                {deltaDir(0, ""), fileEntry("a", []byte{1})},
		"empty entry":                     {deltaDir(0, ""), {}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			fs := &fakeServer{rawManifestParts: []*pb.TreeManifestPart{{Delta: true, Entries: entries}}}
			c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
			_, err := c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{CommitID: "base", View: []byte("v")})
			require.ErrorContains(t, err, "malformed tree manifest")
		})
	}

	t.Run("a delta nobody asked for", func(t *testing.T) {
		fs := &fakeServer{rawManifestParts: []*pb.TreeManifestPart{{Delta: true, Entries: []*pb.TreeManifestEntry{unchangedDir(0, "")}}}}
		c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
		_, err := c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{})
		require.ErrorContains(t, err, "nobody asked for")
	})

	for name, entries := range map[string][]*pb.TreeManifestEntry{
		"file":             {deltaDir(0, ""), deltaDir(1, "ok"), unchangedFile("../escape")},
		"changed file":     {deltaDir(0, ""), fileEntry(".nipa", h32(1))},
		"directory":        {deltaDir(0, ""), deltaDir(1, "ok"), deltaDir(2, "..")},
		"unchanged subdir": {deltaDir(0, ""), unchangedDir(1, "a/b")},
	} {
		t.Run("unsafe "+name, func(t *testing.T) {
			fs := &fakeServer{rawManifestParts: []*pb.TreeManifestPart{{Delta: true, Entries: entries}}}
			c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
			_, err := c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{CommitID: "base", View: []byte("v")})
			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Contains(t, domErr.Message, "unsafe path")
		})
	}
}

func TestClient_GetTreeManifestDelta_Errors(t *testing.T) {
	fs := &fakeServer{treeManifestErr: status.Error(codes.NotFound, "branch \"x\" not found")}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	_, err := c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "x"}, nil, treedelta.Base{})
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)

	notConnected := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	_, err = notConnected.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "x"}, nil, treedelta.Base{})
	require.Error(t, err)

	old := connectedClient(t, &legacyPushServer{}, &stubSession{accessToken: "tok"})
	_, err = old.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "x"}, nil, treedelta.Base{})
	require.ErrorAs(t, err, &domErr)
	require.Contains(t, domErr.Message, "does not support")

	// A retried call starts from scratch.
	fs = &fakeServer{
		manifestErrs:     []error{status.Error(codes.Unauthenticated, "expired")},
		rawManifestParts: []*pb.TreeManifestPart{{Delta: true, View: []byte("v"), Entries: []*pb.TreeManifestEntry{unchangedDir(0, "")}}},
	}
	c = connectedClient(t, fs, &stubSession{accessToken: "old", refreshToken: "new"})
	got, err := c.GetTreeManifestDelta(context.Background(), "org", "project", ManifestTarget{Branch: "main"}, nil, treedelta.Base{CommitID: "b", View: []byte("v")})
	require.NoError(t, err)
	require.True(t, got.Delta.Root.Unchanged)
}
