package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeManifestStream struct {
	grpc.ServerStream
	parts   []*pb.TreeManifestPart
	sendErr error
}

func (s *fakeManifestStream) Context() context.Context { return context.Background() }

func (s *fakeManifestStream) Send(part *pb.TreeManifestPart) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.parts = append(s.parts, part)
	return nil
}

func manifestEntries(parts []*pb.TreeManifestPart) []*pb.TreeManifestEntry {
	var out []*pb.TreeManifestEntry
	for _, p := range parts {
		out = append(out, p.GetEntries()...)
	}
	return out
}

// describe renders the streamed entries as one line each, so tests can check
// the order and depth without building the tree.
func describe(entries []*pb.TreeManifestEntry) []string {
	var out []string
	for _, e := range entries {
		switch x := e.GetEntry().(type) {
		case *pb.TreeManifestEntry_Dir:
			out = append(out, fmt.Sprintf("dir %d %s", x.Dir.GetDepth(), x.Dir.GetName()))
		case *pb.TreeManifestEntry_File:
			out = append(out, fmt.Sprintf("file %s %d", x.File.GetName(), len(x.File.GetChunkHashes())))
		case *pb.TreeManifestEntry_Chunks:
			out = append(out, fmt.Sprintf("chunks %d", len(x.Chunks.GetChunkHashes())))
		}
	}
	return out
}

func TestManifestWriter_DepthFirstOrder(t *testing.T) {
	h := domain.Hash{7}
	root := &domain.TreeNode{Name: "root", Hash: h,
		FileChildren: []*domain.File{{Name: "readme.md", Mode: 0o755, Chunks: []domain.Chunk{{Hash: h, SizeBytes: 3}}}},
		TreeChildren: []*domain.TreeNode{
			{Name: "a", FileChildren: []*domain.File{{Name: "x"}}, TreeChildren: []*domain.TreeNode{{Name: "deep", FileChildren: []*domain.File{{Name: "y"}}}}},
			{Name: "b", FileChildren: []*domain.File{{Name: "z"}}},
		},
	}
	stream := &fakeManifestStream{}
	w := &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{Branch: "main"}}
	require.NoError(t, w.dir(root, 0))
	require.NoError(t, w.flush(true))

	require.Len(t, stream.parts, 1)
	require.Equal(t, "main", stream.parts[0].GetBranch())
	require.Equal(t, []string{
		"dir 0 root", "file readme.md 1",
		"dir 1 a", "file x 0",
		"dir 2 deep", "file y 0",
		"dir 1 b", "file z 0",
	}, describe(stream.parts[0].GetEntries()))

	readme := stream.parts[0].GetEntries()[1].GetFile()
	require.Equal(t, pb.FileMode_FILE_MODE_EXECUTABLE, readme.GetMode())
	require.Equal(t, [][]byte{h[:]}, readme.GetChunkHashes())
	require.Equal(t, []int64{3}, readme.GetChunkSizes())
	require.Equal(t, h[:], stream.parts[0].GetEntries()[0].GetDir().GetTreeHash())
}

func TestManifestWriter_EmptyTreeStillSendsHeader(t *testing.T) {
	stream := &fakeManifestStream{}
	w := &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{Branch: "main"}}
	require.NoError(t, w.dir(nil, 0))
	require.NoError(t, w.flush(true))
	require.Len(t, stream.parts, 1)
	require.Empty(t, stream.parts[0].GetEntries())
	require.Equal(t, "main", stream.parts[0].GetBranch())
}

func TestManifestWriter_SplitsLargeManifests(t *testing.T) {
	root := &domain.TreeNode{Name: "root"}
	for i := 0; i < 20000; i++ {
		root.FileChildren = append(root.FileChildren, &domain.File{
			Name:   fmt.Sprintf("node_modules-package-%05d-index.js", i),
			Chunks: []domain.Chunk{{Hash: domain.Hash{byte(i)}, SizeBytes: 10}, {Hash: domain.Hash{1}, SizeBytes: 5}},
		})
	}
	big := &domain.File{Name: "huge.bin"}
	for i := 0; i < 2*maxManifestChunksEntry+3; i++ {
		big.Chunks = append(big.Chunks, domain.Chunk{Hash: domain.Hash{byte(i), byte(i >> 8), byte(i >> 16)}, SizeBytes: int64(i + 1)})
	}
	noSizes := &domain.File{Name: "nosizes.bin"}
	for i := 0; i < maxManifestChunksEntry+1; i++ {
		noSizes.Chunks = append(noSizes.Chunks, domain.Chunk{Hash: domain.Hash{2}})
	}
	root.FileChildren = append(root.FileChildren, big, noSizes)

	stream := &fakeManifestStream{}
	w := &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{Branch: "main"}}
	require.NoError(t, w.dir(root, 0))
	require.NoError(t, w.flush(true))

	require.Greater(t, len(stream.parts), 3)
	require.Equal(t, "main", stream.parts[0].GetBranch())
	for i, part := range stream.parts {
		require.LessOrEqual(t, proto.Size(part), manifestPartBudget+64, "part %d", i)
		if i > 0 {
			require.Empty(t, part.GetBranch())
		}
	}
	lines := describe(manifestEntries(stream.parts))
	n := len(lines)
	require.Equal(t, []string{
		fmt.Sprintf("file huge.bin %d", maxManifestChunksEntry),
		fmt.Sprintf("chunks %d", maxManifestChunksEntry),
		"chunks 3",
		fmt.Sprintf("file nosizes.bin %d", maxManifestChunksEntry),
		"chunks 1",
	}, lines[n-5:])

	var sizes []int64
	for _, e := range manifestEntries(stream.parts) {
		if f := e.GetFile(); f != nil && f.GetName() == "huge.bin" {
			sizes = append(sizes, f.GetChunkSizes()...)
		}
	}
	entries := manifestEntries(stream.parts)
	for _, e := range entries[len(entries)-4 : len(entries)-2] {
		sizes = append(sizes, e.GetChunks().GetChunkSizes()...)
	}
	require.Len(t, sizes, len(big.Chunks))
	require.Equal(t, int64(len(big.Chunks)), sizes[len(sizes)-1])
	require.Empty(t, entries[len(entries)-1].GetChunks().GetChunkSizes())
	require.Empty(t, entries[len(entries)-2].GetFile().GetChunkSizes())
}

func TestManifestWriter_SendErrorStops(t *testing.T) {
	boom := errors.New("client gone")
	root := &domain.TreeNode{Name: "root"}
	for i := 0; i < 30000; i++ {
		root.TreeChildren = append(root.TreeChildren, &domain.TreeNode{Name: fmt.Sprintf("dir-%05d-with-a-long-name-to-fill-parts", i)})
	}
	stream := &fakeManifestStream{sendErr: boom}
	w := &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{}}
	require.ErrorIs(t, w.dir(root, 0), boom)

	w = &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{}}
	require.ErrorIs(t, w.flush(true), boom)

	file := &domain.File{Name: "huge"}
	for i := 0; i < 2*maxManifestChunksEntry+1; i++ {
		file.Chunks = append(file.Chunks, domain.Chunk{Hash: domain.Hash{1}})
	}
	w = &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{}, size: manifestPartBudget}
	w.part.Entries = []*pb.TreeManifestEntry{{}}
	require.ErrorIs(t, w.file(file), boom)
	big := &domain.TreeNode{Name: "root", FileChildren: []*domain.File{file}}
	w = &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{}}
	require.ErrorIs(t, w.dir(big, 0), boom)
	nested := &domain.TreeNode{Name: "root", TreeChildren: []*domain.TreeNode{big}}
	w = &manifestWriter{send: stream.Send, part: &pb.TreeManifestPart{}}
	require.ErrorIs(t, w.dir(nested, 0), boom)
}

func expectSmallTree(repo *MockbranchRepository, treeID int64) {
	repo.EXPECT().GetTreeNode(gomock.Any(), treeID).Return(&domain.TreeNode{ID: treeID, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), treeID).Return([]*domain.File{{ID: 1, Name: "a.txt", Mode: 0o644}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), treeID).Return(nil, nil)
}

func TestStreamTreeManifest_Branch(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))
	commitID := snow.ID(9)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 42, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(&domain.Commit{ID: commitID, TreeID: 100}, nil)
	expectSmallTree(repo, 100)

	stream := &fakeManifestStream{}
	require.NoError(t, srv.StreamTreeManifest(&pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Branch: "main", Recursive: true,
	}, stream))
	require.Len(t, stream.parts, 1)
	require.Equal(t, "main", stream.parts[0].GetBranch())
	require.Nil(t, stream.parts[0].GetCommit())
	require.Equal(t, []string{"dir 0 root", "file a.txt 0"}, describe(stream.parts[0].GetEntries()))
}

func TestStreamTreeManifest_WholeCommitCarriesDetail(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))
	commitID := snow.ID(9)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 42, TreeID: 100, Message: "seed"}, nil)
	expectSmallTree(repo, 100)

	id := commitID.Base36()
	stream := &fakeManifestStream{}
	require.NoError(t, srv.StreamTreeManifest(&pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"}, CommitId: &id, Recursive: true,
	}, stream))
	require.Len(t, stream.parts, 1)
	detail := stream.parts[0].GetCommit()
	require.NotNil(t, detail)
	require.Equal(t, id, detail.GetCommitId())
	require.Equal(t, "seed", detail.GetMessage())
	require.Len(t, detail.GetTreeHash(), 64)
	require.Equal(t, []string{"dir 0 root", "file a.txt 0"}, describe(stream.parts[0].GetEntries()))
}

func TestStreamTreeManifest_CommitSubdirHasNoDetail(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))
	commitID := snow.ID(9)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(&domain.Commit{ID: commitID, ProjectID: 42, TreeID: 100}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return([]*domain.File{{ID: 1, Name: "a.txt", Mode: 0o644}}, nil)

	id := commitID.Base36()
	stream := &fakeManifestStream{}
	require.NoError(t, srv.StreamTreeManifest(&pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"}, CommitId: &id,
	}, stream))
	require.Nil(t, stream.parts[0].GetCommit())
}

func TestStreamTreeManifest_Errors(t *testing.T) {
	bad := "!!"
	missing := snow.ID(5).Base36()
	cases := []struct {
		name  string
		req   *pb.GetTreeManifestRequest
		setup func(*MockpermissionUsecase, *MockbranchRepository)
		code  codes.Code
	}{
		{name: "unknown project", req: &pb.GetTreeManifestRequest{Context: &pb.ProjectContext{Org: "org", Project: "nope"}}, code: codes.Internal},
		{name: "invalid commit id", req: &pb.GetTreeManifestRequest{Context: &pb.ProjectContext{Org: "org", Project: "proj"}, CommitId: &bad, Recursive: true}, code: codes.InvalidArgument},
		{name: "invalid commit id for a subtree", req: &pb.GetTreeManifestRequest{Context: &pb.ProjectContext{Org: "org", Project: "proj"}, CommitId: &bad}, code: codes.InvalidArgument},
		{
			name: "commit not found",
			req:  &pb.GetTreeManifestRequest{Context: &pb.ProjectContext{Org: "org", Project: "proj"}, CommitId: &missing, Recursive: true},
			setup: func(perm *MockpermissionUsecase, repo *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
				repo.EXPECT().GetCommit(gomock.Any(), snow.ID(5)).Return(nil, domain.NewErrorNotFound("commit"))
			},
			code: codes.NotFound,
		},
		{
			name: "no read access",
			req:  &pb.GetTreeManifestRequest{Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Branch: "main"},
			setup: func(perm *MockpermissionUsecase, _ *MockbranchRepository) {
				perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(false)
			},
			code: codes.PermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch, perm, repo := newTestBranchUc(t)
			if tc.setup != nil {
				tc.setup(perm, repo)
			}
			srv := New(newMockUsecaseContainer(t, branch))
			stream := &fakeManifestStream{}
			err := srv.StreamTreeManifest(tc.req, stream)
			require.Equal(t, tc.code, status.Code(err), err)
			require.Empty(t, stream.parts)
		})
	}
}
