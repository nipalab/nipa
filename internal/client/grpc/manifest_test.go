package grpc

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

// StreamTreeManifest serves the fake's unary manifest (or, for a whole
// commit, its GetCommit answer) one entry per part.
func (f *fakeServer) StreamTreeManifest(req *pb.GetTreeManifestRequest, stream grpc.ServerStreamingServer[pb.TreeManifestPart]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.manifestAuth = append(f.manifestAuth, md.Get("authorization")...)
	if len(f.manifestErrs) > 0 {
		err := f.manifestErrs[0]
		f.manifestErrs = f.manifestErrs[1:]
		return err
	}
	f.lastBranch = req.GetBranch()
	f.lastRecursive = req.GetRecursive()
	f.lastPaths = req.GetPaths()
	f.lastTreeHash = req.GetTreeHash()
	f.lastCommitID = req.GetCommitId()
	tree := f.treeManifest
	first := &pb.TreeManifestPart{Branch: "main"}
	if req.GetCommitId() != "" && req.GetRecursive() && (f.getCommitResp != nil || f.getCommitErr != nil) {
		f.lastGetCommitReq = &pb.GetCommitRequest{Context: req.GetContext(), CommitId: req.GetCommitId()}
		if f.getCommitErr != nil {
			return f.getCommitErr
		}
		tree = f.getCommitResp.GetRootTree()
		first.Commit = f.getCommitResp.GetCommit()
	} else if f.treeManifestErr != nil {
		return f.treeManifestErr
	}
	if f.rawManifestParts != nil {
		for _, part := range f.rawManifestParts {
			if err := stream.Send(part); err != nil {
				return err
			}
		}
		return nil
	}
	entries := encodeManifest(tree, 0)
	if len(entries) == 0 {
		return stream.Send(first)
	}
	for i, e := range entries {
		part := &pb.TreeManifestPart{Entries: []*pb.TreeManifestEntry{e}}
		if i == 0 {
			first.Entries = part.Entries
			part = first
		}
		if err := stream.Send(part); err != nil {
			return err
		}
	}
	return nil
}

// rawHex encodes a hex hash the way the server sends domain.Hash: an empty
// hash is the 32-byte zero hash.
func rawHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return make([]byte, 32)
	}
	return b
}

func encodeManifest(node *pb.TreeManifest, depth int32) []*pb.TreeManifestEntry {
	if node == nil {
		return nil
	}
	out := []*pb.TreeManifestEntry{{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{
		Depth: depth, Name: node.GetPath(), TreeHash: rawHex(node.GetTreeHash()),
	}}}}
	for _, f := range node.GetFiles() {
		hashes := make([][]byte, len(f.GetChunkHashes()))
		for i, h := range f.GetChunkHashes() {
			hashes[i] = rawHex(h)
		}
		out = append(out, &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_File{File: &pb.TreeManifestFile{
			Name: f.GetPath(), Mode: f.GetMode(), SizeBytes: f.GetSizeBytes(), IsBinary: f.GetIsBinary(),
			Encoding: f.GetEncoding(), ChunkHashes: hashes, ChunkSizes: f.GetChunkSizes(),
		}}})
	}
	for _, sub := range node.GetSubTrees() {
		out = append(out, encodeManifest(sub, depth+1)...)
	}
	return out
}

func h32(b byte) []byte {
	out := make([]byte, 32)
	out[0] = b
	return out
}

func dirEntry(depth int32, name string) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{Depth: depth, Name: name, TreeHash: h32(byte(depth))}}}
}

func fileEntry(name string, hashes ...[]byte) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_File{File: &pb.TreeManifestFile{Name: name, ChunkHashes: hashes}}}
}

func chunksEntry(sizes []int64, hashes ...[]byte) *pb.TreeManifestEntry {
	return &pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Chunks{Chunks: &pb.TreeManifestChunks{ChunkHashes: hashes, ChunkSizes: sizes}}}
}

func TestManifestAssembler_RebuildsTree(t *testing.T) {
	var a manifestAssembler
	for _, e := range []*pb.TreeManifestEntry{
		dirEntry(0, "root"),
		fileEntry("readme"),
		dirEntry(1, "a"),
		fileEntry("x", h32(1)),
		chunksEntry([]int64{5}, h32(2)),
		dirEntry(2, "deep"),
		dirEntry(3, "deeper"),
		fileEntry("y"),
		dirEntry(1, "b"),
		fileEntry("z"),
	} {
		require.NoError(t, a.add(e))
	}
	root := a.root
	require.Equal(t, "root", root.GetPath())
	require.Equal(t, hex.EncodeToString(h32(0)), root.GetTreeHash())
	require.Equal(t, "readme", root.GetFiles()[0].GetPath())
	require.Len(t, root.GetSubTrees(), 2)
	a1 := root.GetSubTrees()[0]
	require.Equal(t, "a", a1.GetPath())
	require.Equal(t, []string{hex.EncodeToString(h32(1)), hex.EncodeToString(h32(2))}, a1.GetFiles()[0].GetChunkHashes())
	require.Equal(t, []int64{5}, a1.GetFiles()[0].GetChunkSizes())
	require.Equal(t, "deeper", a1.GetSubTrees()[0].GetSubTrees()[0].GetPath())
	require.Equal(t, "y", a1.GetSubTrees()[0].GetSubTrees()[0].GetFiles()[0].GetPath())
	require.Equal(t, "b", root.GetSubTrees()[1].GetPath())
	require.Equal(t, "z", root.GetSubTrees()[1].GetFiles()[0].GetPath())
}

func TestManifestAssembler_RejectsMalformedStreams(t *testing.T) {
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
			var a manifestAssembler
			var err error
			for _, e := range entries {
				if err = a.add(e); err != nil {
					break
				}
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), "malformed tree manifest")
		})
	}
}

func TestClient_StreamTreeManifest_MultiPart(t *testing.T) {
	fs := &fakeServer{rawManifestParts: []*pb.TreeManifestPart{
		{Branch: "dev", Entries: []*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("big", h32(1))}},
		{Entries: []*pb.TreeManifestEntry{chunksEntry(nil, h32(2), h32(3))}},
		{Branch: "ignored", Entries: []*pb.TreeManifestEntry{dirEntry(1, "sub"), fileEntry("small")}},
	}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})

	res, commit, err := c.StreamTreeManifest(context.Background(), &pb.GetTreeManifestRequest{Branch: "dev", Recursive: true})
	require.NoError(t, err)
	require.Nil(t, commit)
	require.Equal(t, "dev", res.GetBranch())
	require.Len(t, res.GetRootTree().GetFiles()[0].GetChunkHashes(), 3)
	require.Equal(t, "small", res.GetRootTree().GetSubTrees()[0].GetFiles()[0].GetPath())
	require.Equal(t, []string{"Bearer tok"}, fs.manifestAuth)

	tree, err := c.GetTreeNodeManifest(context.Background(), "org", "project", "dev", nil)
	require.NoError(t, err)
	require.Len(t, tree.FileChildren[0].Chunks, 3)
	require.Equal(t, "sub", tree.TreeChildren[0].Name)
}

func TestClient_StreamTreeManifest_EmptyTreeIsNil(t *testing.T) {
	fs := &fakeServer{}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	tree, err := c.GetTreeNodeManifest(context.Background(), "org", "project", "main", nil)
	require.NoError(t, err)
	require.Nil(t, tree)
}

func TestClient_StreamTreeManifest_MalformedStream(t *testing.T) {
	fs := &fakeServer{rawManifestParts: []*pb.TreeManifestPart{{Entries: []*pb.TreeManifestEntry{fileEntry("orphan")}}}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	_, err := c.GetTreeNodeManifest(context.Background(), "org", "project", "main", nil)
	require.ErrorContains(t, err, "malformed tree manifest")
}

func TestClient_StreamTreeManifest_RefreshesExpiredToken(t *testing.T) {
	fs := &fakeServer{
		manifestErrs: []error{status.Error(codes.Unauthenticated, "expired")},
		treeManifest: &pb.TreeManifest{Path: "root", TreeHash: hex.EncodeToString(h32(9))},
	}
	session := &stubSession{accessToken: "old", refreshToken: "new"}
	c := connectedClient(t, fs, session)

	tree, err := c.GetCommitTreeManifest(context.Background(), "org", "project", "abc", []string{"assets"})
	require.NoError(t, err)
	require.Equal(t, "root", tree.Name)
	require.Equal(t, []string{"Bearer old", "Bearer new"}, fs.manifestAuth)
	require.Equal(t, "abc", fs.lastCommitID)
	require.Equal(t, []string{"assets"}, fs.lastPaths)
}

func TestClient_StreamTreeManifest_Errors(t *testing.T) {
	fs := &fakeServer{treeManifestErr: status.Error(codes.NotFound, "branch \"x\" not found")}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	_, err := c.GetTreeNodeManifest(context.Background(), "org", "project", "x", nil)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)

	notConnected := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	_, _, err = notConnected.StreamTreeManifest(context.Background(), &pb.GetTreeManifestRequest{})
	require.EqualError(t, err, "not connected to a nipa server")

	noToken := connectedClient(t, &fakeServer{}, &stubSession{getTokenErr: errors.New("no token")})
	_, err = noToken.GetTreeNodeManifest(context.Background(), "org", "project", "main", nil)
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 401, domErr.Code)
}

func TestClient_StreamTreeManifest_ServerWithoutStreaming(t *testing.T) {
	c := connectedClient(t, &legacyPushServer{}, &stubSession{accessToken: "tok"})
	for name, call := range map[string]func() error{
		"branch": func() error {
			_, err := c.GetTreeNodeManifest(context.Background(), "org", "project", "main", nil)
			return err
		},
		"commit": func() error {
			_, err := c.GetCommit(context.Background(), "org", "project", "abc")
			return err
		},
	} {
		var domErr *domain.Error
		require.ErrorAs(t, call(), &domErr, name)
		require.Equal(t, "the server does not support streamed tree manifests", domErr.Message, name)
		require.Contains(t, domErr.Hint, "upgrade the nipa server", name)
	}
}

func TestManifestAssembler_RejectsUnsafeNames(t *testing.T) {
	cases := map[string]struct {
		entries []*pb.TreeManifestEntry
		path    string
	}{
		"parent file":         {[]*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("../escaped.bin")}, `"../escaped.bin"`},
		"dot dot file":        {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "a"), fileEntry("..")}, `"a/.."`},
		"dot dot dir":         {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "a"), dirEntry(2, "..")}, `"a/.."`},
		"slash in name":       {[]*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("a/b")}, `"a/b"`},
		"backslash in name":   {[]*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry(`..\x`)}, `"..\\x"`},
		"empty file name":     {[]*pb.TreeManifestEntry{dirEntry(0, "root"), fileEntry("")}, `""`},
		"metadata dir":        {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, ".nipa"), fileEntry("config")}, `".nipa"`},
		"metadata upper case": {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "sub"), dirEntry(2, ".NIPA")}, `"sub/.NIPA"`},
		"nul byte":            {[]*pb.TreeManifestEntry{dirEntry(0, "root"), dirEntry(1, "d"), dirEntry(2, "e"), fileEntry("a\x00")}, `"d/e/a\x00"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var a manifestAssembler
			var err error
			for _, e := range tc.entries {
				if err = a.add(e); err != nil {
					break
				}
			}
			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Equal(t, 400, domErr.Code)
			require.Contains(t, domErr.Message, "unsafe path "+tc.path)
		})
	}
}

func TestManifestAssembler_RootNameIsALabel(t *testing.T) {
	for _, root := range []string{"", ".", "..", ".nipa", "a/b"} {
		var a manifestAssembler
		require.NoError(t, a.add(dirEntry(0, root)), root)
		require.NoError(t, a.add(fileEntry("ok.txt")), root)
	}
}

func TestClient_StreamTreeManifest_RejectsUnsafeNames(t *testing.T) {
	fs := &fakeServer{treeManifest: &pb.TreeManifest{Path: "root", Files: []*pb.FileNode{{Path: "../escaped.bin"}}}}
	c := connectedClient(t, fs, &stubSession{accessToken: "tok"})
	_, err := c.GetTreeNodeManifest(context.Background(), "org", "project", "main", nil)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Contains(t, domErr.Message, `unsafe path "../escaped.bin"`)
}

func TestValidateManifestNames(t *testing.T) {
	require.NoError(t, validateManifestNames(nil))
	ok := &pb.TreeManifest{Path: "..", Files: []*pb.FileNode{{Path: "a"}}, SubTrees: []*pb.TreeManifest{{Path: "d", Files: []*pb.FileNode{{Path: "b"}}}}}
	require.NoError(t, validateManifestNames(ok))

	badFile := &pb.TreeManifest{Path: "root", SubTrees: []*pb.TreeManifest{{Path: "d", SubTrees: []*pb.TreeManifest{{Path: "e", Files: []*pb.FileNode{{Path: ".."}}}}}}}
	require.ErrorContains(t, validateManifestNames(badFile), `unsafe path "d/e/.."`)
	badDir := &pb.TreeManifest{Path: "root", SubTrees: []*pb.TreeManifest{{Path: ".Nipa"}}}
	require.ErrorContains(t, validateManifestNames(badDir), `unsafe path ".Nipa"`)
}
