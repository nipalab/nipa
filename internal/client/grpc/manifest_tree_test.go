package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

func sizedFileEntry(name string, sizes []int64, hashes ...[]byte) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_File{File: &pb.TreeManifestFile{
		Name: name, Mode: 2, SizeBytes: 9, IsBinary: true, Encoding: "raw", ChunkHashes: hashes, ChunkSizes: sizes,
	}}}
}

func assembleTree(entries []*pb.TreeManifestEntry) (*serverDomain.TreeNode, error) {
	var a treeAssembler
	for _, e := range entries {
		if err := a.add(e); err != nil {
			return nil, err
		}
	}
	return a.finish(), nil
}

// The domain tree read directly must equal the one converted from the
// protobuf tree the other assembler builds from the same entries.
func TestTreeAssembler_MatchesConvertedManifest(t *testing.T) {
	entries := []*pb.TreeManifestEntry{
		dirEntry(0, "root"),
		fileEntry("readme"),
		sizedFileEntry("sized.bin", []int64{4, 5}, h32(7), h32(8)),
		dirEntry(1, "a"),
		sizedFileEntry("x", []int64{3}, h32(1)),
		chunksEntry([]int64{5}, h32(2)),
		dirEntry(2, "deep"),
		dirEntry(3, "deeper"),
		fileEntry("y", h32(3)),
		dirEntry(1, "b"),
		fileEntry("z"),
	}
	var viaProto manifestAssembler
	for _, e := range entries {
		require.NoError(t, viaProto.add(e))
	}

	got, err := assembleTree(entries)
	require.NoError(t, err)
	require.Equal(t, toServerTreeNode(viaProto.root), got)

	x := got.TreeChildren[0].FileChildren[0]
	require.Equal(t, []int64{3, 5}, []int64{x.Chunks[0].SizeBytes, x.Chunks[1].SizeBytes}, "a continuation extends the file's chunks and sizes")
	require.Equal(t, chunker.FileHash([]serverDomain.Hash{x.Chunks[0].Hash, x.Chunks[1].Hash}), x.Hash, "the file hash covers the continuation")
}

func TestTreeAssembler_DropsSizesUnlessEveryPartHasThem(t *testing.T) {
	got, err := assembleTree([]*pb.TreeManifestEntry{
		dirEntry(0, "root"),
		sizedFileEntry("x", []int64{3}, h32(1)),
		chunksEntry(nil, h32(2)),
		sizedFileEntry("y", []int64{4}, h32(3)),
	})
	require.NoError(t, err)
	x, y := got.FileChildren[0], got.FileChildren[1]
	require.Zero(t, x.Chunks[0].SizeBytes, "a file with a part from a server without chunk sizes carries none")
	require.Zero(t, x.Chunks[1].SizeBytes)
	require.Equal(t, int64(4), y.Chunks[0].SizeBytes, "the next file is sized on its own")
}

func TestTreeAssembler_EmptyStreamIsNil(t *testing.T) {
	got, err := assembleTree(nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestTreeAssembler_RejectsMalformedStreams(t *testing.T) {
	cases := map[string][]*pb.TreeManifestEntry{
		"file before a directory":  {fileEntry("x")},
		"chunks before a file":     {dirEntry(0, "root"), chunksEntry(nil, h32(1))},
		"chunks after a directory": {dirEntry(0, "root"), fileEntry("x"), dirEntry(1, "a"), chunksEntry(nil, h32(1))},
		"second root":              {dirEntry(0, "root"), dirEntry(0, "again")},
		"skipped depth":            {dirEntry(0, "root"), dirEntry(2, "deep")},
		"child before root":        {dirEntry(1, "a")},
		"empty entry":              {{}},
		"short tree hash":          {{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{TreeHash: []byte{1}}}}},
		"short file chunk hash":    {dirEntry(0, "root"), fileEntry("x", []byte{1})},
		"short continuation hash":  {dirEntry(0, "root"), fileEntry("x"), chunksEntry(nil, []byte{1})},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := assembleTree(entries)
			require.ErrorContains(t, err, "malformed tree manifest")
		})
	}
}

func TestTreeAssembler_RejectsUnsafeNames(t *testing.T) {
	cases := map[string]struct {
		entries []*pb.TreeManifestEntry
		path    string
	}{
		"parent file":         {[]*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("../escaped.bin")}, `"../escaped.bin"`},
		"dot dot dir":         {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "a"), dirEntry(2, "..")}, `"a/.."`},
		"slash in name":       {[]*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("a/b")}, `"a/b"`},
		"metadata dir":        {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, ".nipa"), fileEntry("config")}, `".nipa"`},
		"metadata upper case": {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "sub"), dirEntry(2, ".NIPA")}, `"sub/.NIPA"`},
		"nested file":         {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "d"), dirEntry(2, "e"), fileEntry("a\x00")}, `"d/e/a\x00"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := assembleTree(tc.entries)
			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Equal(t, 400, domErr.Code)
			require.Contains(t, domErr.Message, "unsafe path "+tc.path)
		})
	}

	got, err := assembleTree([]*pb.TreeManifestEntry{dirEntry(0, ".."), fileEntry("ok")})
	require.NoError(t, err, "the root name is a label, never part of a path")
	require.Equal(t, "..", got.Name)
}

func TestClient_TreeManifest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	_, err := c.GetTreeNodeManifest(context.Background(), "org", "proj", "main", nil)
	require.Error(t, err)
}
