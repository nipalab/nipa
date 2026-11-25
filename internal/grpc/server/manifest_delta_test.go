package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// describeDelta is describe with the marks of a delta.
func describeDelta(entries []*pb.TreeManifestEntry) []string {
	var out []string
	for _, e := range entries {
		switch x := e.GetEntry().(type) {
		case *pb.TreeManifestEntry_Dir:
			line := fmt.Sprintf("dir %d %s", x.Dir.GetDepth(), x.Dir.GetName())
			if x.Dir.GetUnchanged() {
				line += " unchanged"
			}
			if len(x.Dir.GetTreeHash()) > 0 {
				line += " hashed"
			}
			out = append(out, line)
		case *pb.TreeManifestEntry_File:
			if x.File.GetUnchanged() {
				out = append(out, fmt.Sprintf("file %s unchanged", x.File.GetName()))
			} else {
				out = append(out, fmt.Sprintf("file %s %d", x.File.GetName(), len(x.File.GetChunkHashes())))
			}
		case *pb.TreeManifestEntry_Chunks:
			out = append(out, fmt.Sprintf("chunks %d", len(x.Chunks.GetChunkHashes())))
		}
	}
	return out
}

func TestManifestWriter_Delta(t *testing.T) {
	huge := &domain.File{Name: "huge.bin", Mode: 0o755, SizeBytes: 9, IsBinary: true, Encoding: "zstd"}
	for i := 0; i < maxManifestChunksEntry+2; i++ {
		huge.Chunks = append(huge.Chunks, domain.Chunk{Hash: domain.Hash{byte(i), byte(i >> 8)}, SizeBytes: 1})
	}
	hash := domain.Hash{4}
	delta := &usecase.ManifestDelta{
		Commit: &domain.Commit{ID: 9, Message: "target"},
		Delta: &treedelta.Delta{RootHash: &hash, Root: &treedelta.Dir{
			Name:  "root",
			Files: []treedelta.File{{Name: "keep.txt"}, {Name: "huge.bin", Changed: huge}},
			Dirs: []*treedelta.Dir{
				{Name: "assets", Unchanged: true},
				{Name: "src", Files: []treedelta.File{{Name: "main.go", Changed: &domain.File{Name: "main.go"}}}, Dirs: []*treedelta.Dir{{Name: "lib", Unchanged: true}}},
			},
		}},
	}
	commitID := snow.ID(9).Base36()
	req := &pb.GetTreeManifestRequest{CommitId: &commitID, Recursive: true, BaseView: []byte("view")}
	stream := &fakeManifestStream{}
	require.NoError(t, sendManifestDelta(req, &pb.TreeManifestPart{}, delta, stream.Send))

	first := stream.parts[0]
	require.True(t, first.GetDelta())
	require.Equal(t, []byte("view"), first.GetView())
	require.Equal(t, commitID, first.GetTargetCommitId())
	require.Equal(t, "target", first.GetCommit().GetMessage())
	require.Equal(t, hash.String(), first.GetCommit().GetTreeHash())
	entries := manifestEntries(stream.parts)
	require.Equal(t, []string{
		"dir 0 root hashed",
		"file keep.txt unchanged",
		fmt.Sprintf("file huge.bin %d", maxManifestChunksEntry), "chunks 2",
		"dir 1 assets unchanged",
		"dir 1 src",
		"file main.go 0",
		"dir 2 lib unchanged",
	}, describeDelta(entries))
	require.Equal(t, hash[:], entries[0].GetDir().GetTreeHash())
	require.True(t, proto.Equal(&pb.TreeManifestFile{Name: "keep.txt", Unchanged: true}, entries[1].GetFile()), "an unchanged file is its name")
	sent := entries[2].GetFile()
	require.Equal(t, pb.FileMode_FILE_MODE_EXECUTABLE, sent.GetMode())
	require.True(t, sent.GetIsBinary())
	require.Equal(t, "zstd", sent.GetEncoding())
	require.False(t, sent.GetUnchanged())

	// Without a provable root no directory carries a hash, and a request for
	// part of a commit carries no commit detail.
	delta.Delta.RootHash = nil
	stream = &fakeManifestStream{}
	require.NoError(t, sendManifestDelta(req, &pb.TreeManifestPart{}, delta, stream.Send))
	require.Empty(t, stream.parts[0].GetCommit().GetTreeHash())
	require.Equal(t, "dir 0 root", describeDelta(stream.parts[0].GetEntries())[0])
	stream = &fakeManifestStream{}
	require.NoError(t, sendManifestDelta(&pb.GetTreeManifestRequest{Branch: "main", Recursive: true}, &pb.TreeManifestPart{Branch: "main"}, delta, stream.Send))
	require.Nil(t, stream.parts[0].GetCommit())
	require.Equal(t, "main", stream.parts[0].GetBranch())

	// An unchanged root is one entry.
	stream = &fakeManifestStream{}
	unchanged := &usecase.ManifestDelta{Commit: delta.Commit, Delta: &treedelta.Delta{Root: &treedelta.Dir{Unchanged: true}}}
	require.NoError(t, sendManifestDelta(req, &pb.TreeManifestPart{}, unchanged, stream.Send))
	require.Equal(t, []string{"dir 0  unchanged"}, describeDelta(manifestEntries(stream.parts)))

	// A failed send stops the stream wherever it happens.
	boom := errors.New("client gone")
	for budget := 0; budget < 3; budget++ {
		sends := 0
		send := func(*pb.TreeManifestPart) error {
			if sends++; sends > budget {
				return boom
			}
			return nil
		}
		wide := &treedelta.Dir{Name: "root"}
		for i := 0; i < 40000; i++ {
			wide.Files = append(wide.Files, treedelta.File{Name: fmt.Sprintf("unchanged-file-with-a-long-name-%06d", i)})
		}
		for i := 0; i < 40000; i++ {
			wide.Dirs = append(wide.Dirs, &treedelta.Dir{Name: fmt.Sprintf("unchanged-directory-with-a-long-name-%06d", i), Unchanged: true})
		}
		err := sendManifestDelta(req, &pb.TreeManifestPart{}, &usecase.ManifestDelta{Commit: delta.Commit, Delta: &treedelta.Delta{Root: wide}}, send)
		require.ErrorIs(t, err, boom, "budget %d", budget)
	}
	err := sendManifestDelta(req, &pb.TreeManifestPart{}, delta, func(*pb.TreeManifestPart) error { return boom })
	require.ErrorIs(t, err, boom)
}

// deltaServerFixture is a project with two commits whose stored hashes are
// sound: base (commit 8, tree 90) and target (commit 9, tree 100) differ in
// a.txt and share the directory "lib".
type deltaServerFixture struct {
	srv                    pb.NipaServiceServer
	branch                 *usecase.Branch
	perm                   *MockpermissionUsecase
	repo                   *MockbranchRepository
	baseRoot, targetRoot   domain.Hash
	baseID, targetID, view string
}

func newDeltaServerFixture(t *testing.T) *deltaServerFixture {
	t.Helper()
	branch, perm, repo := newTestBranchUc(t)
	f := &deltaServerFixture{branch: branch, perm: perm, repo: repo, baseID: snow.ID(8).Base36(), targetID: snow.ID(9).Base36()}
	f.srv = New(newMockUsecaseContainer(t, branch))
	view, err := branch.ManifestView(context.Background(), 42, nil, true)
	require.NoError(t, err)
	f.view = string(view)

	lib := domain.Hash{0x11}
	oldFile, newFile := domain.Hash{1}, domain.Hash{2}
	f.baseRoot = treehash.TreeHash([]treehash.FileEntry{{Name: "a.txt", Hash: oldFile, Mode: 0o644}, {Name: "b.txt", Hash: domain.Hash{3}, Mode: 0o644}}, []treehash.TreeEntry{{Name: "lib", Hash: lib}})
	f.targetRoot = treehash.TreeHash([]treehash.FileEntry{{Name: "a.txt", Hash: newFile, Mode: 0o644}, {Name: "b.txt", Hash: domain.Hash{3}, Mode: 0o644}}, []treehash.TreeEntry{{Name: "lib", Hash: lib}})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(8)).Return(&domain.Commit{ID: 8, ProjectID: 42, TreeID: 90, Message: "base"}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).Return(&domain.Commit{ID: 9, ProjectID: 42, TreeID: 100, Message: "target"}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(7)).Return(&domain.Commit{ID: 7, ProjectID: 1, TreeID: 80}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorNotFound("commit")).AnyTimes()
	head := snow.ID(9)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 1, ProjectID: 42, Name: "main", CommitID: &head}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(90)).DoAndReturn(func(context.Context, int64) (*domain.TreeNode, error) {
		return &domain.TreeNode{ID: 90, Name: "root", Hash: f.baseRoot}, nil
	}).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).DoAndReturn(func(context.Context, int64) (*domain.TreeNode, error) {
		return &domain.TreeNode{ID: 100, Name: "root", Hash: f.targetRoot}, nil
	}).AnyTimes()
	files := func() map[int64][]*domain.File {
		return map[int64][]*domain.File{
			90:  {{ID: 1, Name: "a.txt", Mode: 0o644, Hash: oldFile, SizeBytes: 3}, {ID: 2, Name: "b.txt", Mode: 0o644, Hash: domain.Hash{3}}},
			100: {{ID: 3, Name: "a.txt", Mode: 0o644, Hash: newFile, SizeBytes: 4}, {ID: 2, Name: "b.txt", Mode: 0o644, Hash: domain.Hash{3}}},
			91:  {{ID: 5, Name: "x.go", Mode: 0o644, Hash: domain.Hash{5}}},
			101: {{ID: 5, Name: "x.go", Mode: 0o644, Hash: domain.Hash{5}}},
		}
	}
	children := func() map[int64][]*domain.TreeNode {
		return map[int64][]*domain.TreeNode{
			90:  {{ID: 91, Name: "lib", Hash: lib}},
			100: {{ID: 101, Name: "lib", Hash: lib}},
		}
	}
	pick := func(ids []int64, all map[int64][]*domain.File) map[int64][]*domain.File {
		out := map[int64][]*domain.File{}
		for _, id := range ids {
			if rows, ok := all[id]; ok {
				out[id] = rows
			}
		}
		return out
	}
	repo.EXPECT().ListFileMetaByTrees(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []int64) (map[int64][]*domain.File, error) {
		return pick(ids, files()), nil
	}).AnyTimes()
	repo.EXPECT().ListFilesByTrees(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []int64) (map[int64][]*domain.File, error) {
		out := pick(ids, files())
		for _, rows := range out {
			for _, file := range rows {
				if file.ID == 3 {
					file.Chunks = []domain.Chunk{{ID: 30, Hash: domain.Hash{0x30}, SizeBytes: 4}}
				}
			}
		}
		return out, nil
	}).AnyTimes()
	repo.EXPECT().ListTreeChildrenByTrees(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []int64) (map[int64][]*domain.TreeNode, error) {
		all, out := children(), map[int64][]*domain.TreeNode{}
		for _, id := range ids {
			if rows, ok := all[id]; ok {
				out[id] = rows
			}
		}
		return out, nil
	}).AnyTimes()
	repo.EXPECT().ListChunksByFiles(gomock.Any(), []int64{3}).Return(map[int64][]domain.Chunk{3: {{ID: 30, Hash: domain.Hash{0x30}, SizeBytes: 4}}}, nil).AnyTimes()
	return f
}

func (f *deltaServerFixture) stream(t *testing.T, req *pb.GetTreeManifestRequest) []*pb.TreeManifestPart {
	t.Helper()
	req.Context = &pb.ProjectContext{Org: "org", Project: "proj"}
	stream := &fakeManifestStream{}
	require.NoError(t, f.srv.StreamTreeManifest(req, stream))
	return stream.parts
}

func TestStreamTreeManifest_Delta(t *testing.T) {
	f := newDeltaServerFixture(t)

	// The commit form, naming the base root hash: a delta with a root hash.
	parts := f.stream(t, &pb.GetTreeManifestRequest{
		CommitId: &f.targetID, Recursive: true,
		BaseCommitId: &f.baseID, BaseView: []byte(f.view), BaseTreeHash: f.baseRoot[:],
	})
	require.Len(t, parts, 1)
	first := parts[0]
	require.True(t, first.GetDelta())
	require.Equal(t, f.view, string(first.GetView()))
	require.Equal(t, f.targetID, first.GetTargetCommitId())
	require.Equal(t, "target", first.GetCommit().GetMessage())
	require.Equal(t, f.targetRoot.String(), first.GetCommit().GetTreeHash())
	require.Equal(t, []string{"dir 0 root hashed", "file a.txt 1", "file b.txt unchanged", "dir 1 lib unchanged"}, describeDelta(first.GetEntries()))
	require.Equal(t, f.targetRoot[:], first.GetEntries()[0].GetDir().GetTreeHash())
	changed := first.GetEntries()[1].GetFile()
	require.Equal(t, int64(4), changed.GetSizeBytes())
	chunkHash := domain.Hash{0x30}
	require.Equal(t, [][]byte{chunkHash[:]}, changed.GetChunkHashes())

	// The branch form leads to the head; without the base root hash, or with
	// one of the wrong length, the delta has none.
	for _, baseHash := range [][]byte{nil, {1, 2, 3}} {
		parts = f.stream(t, &pb.GetTreeManifestRequest{Branch: "main", Recursive: true, BaseCommitId: &f.baseID, BaseView: []byte(f.view), BaseTreeHash: baseHash})
		first = parts[0]
		require.True(t, first.GetDelta())
		require.Equal(t, "main", first.GetBranch())
		require.Equal(t, f.targetID, first.GetTargetCommitId())
		require.Nil(t, first.GetCommit())
		require.Equal(t, []string{"dir 0 root", "file a.txt 1", "file b.txt unchanged", "dir 1 lib unchanged"}, describeDelta(first.GetEntries()))
	}

	// Base and target are the same commit: an unchanged root.
	parts = f.stream(t, &pb.GetTreeManifestRequest{CommitId: &f.baseID, Recursive: true, BaseCommitId: &f.baseID, BaseView: []byte(f.view), BaseTreeHash: f.baseRoot[:]})
	require.True(t, parts[0].GetDelta())
	require.Equal(t, []string{"dir 0 root unchanged hashed"}, describeDelta(parts[0].GetEntries()))
}

func TestStreamTreeManifest_DeclinedDeltaIsThePlainManifest(t *testing.T) {
	f := newDeltaServerFixture(t)
	bad, unknown, foreign := "!!", snow.ID(5).Base36(), snow.ID(7).Base36()

	for _, form := range []struct {
		name string
		req  func() *pb.GetTreeManifestRequest
	}{
		{"whole commit", func() *pb.GetTreeManifestRequest {
			return &pb.GetTreeManifestRequest{CommitId: &f.targetID, Recursive: true}
		}},
		{"branch", func() *pb.GetTreeManifestRequest { return &pb.GetTreeManifestRequest{Branch: "main", Recursive: true} }},
		{"not recursive", func() *pb.GetTreeManifestRequest { return &pb.GetTreeManifestRequest{Branch: "main"} }},
	} {
		// What the server sent before deltas existed: the same entries in
		// the same parts, the branch and the commit detail. Only the view
		// token is new.
		plain := f.stream(t, form.req())
		require.NotEmpty(t, plain, form.name)
		require.False(t, plain[0].GetDelta(), form.name)
		require.Empty(t, plain[0].GetTargetCommitId(), form.name)
		view, err := f.branch.ManifestView(context.Background(), 42, nil, form.req().GetRecursive())
		require.NoError(t, err)
		require.Equal(t, view, plain[0].GetView(), form.name)
		legacy := &fakeManifestStream{}
		w := &manifestWriter{send: legacy.Send, part: &pb.TreeManifestPart{Branch: form.req().GetBranch(), Commit: plain[0].GetCommit()}}
		root := &domain.TreeNode{Name: "root", FileChildren: []*domain.File{
			{Name: "a.txt", Mode: 0o644, SizeBytes: 4, Chunks: []domain.Chunk{{Hash: domain.Hash{0x30}, SizeBytes: 4}}}, {Name: "b.txt", Mode: 0o644},
		}}
		if form.req().GetRecursive() {
			root.TreeChildren = []*domain.TreeNode{{Name: "lib", FileChildren: []*domain.File{{Name: "x.go", Mode: 0o644}}}}
		}
		require.NoError(t, w.dir(root, 0))
		require.NoError(t, w.flush(true))
		require.Len(t, plain, len(legacy.parts), form.name)
		for i := range plain {
			got := proto.Clone(plain[i]).(*pb.TreeManifestPart)
			got.View = nil
			for _, e := range append(got.GetEntries(), legacy.parts[i].GetEntries()...) {
				if d := e.GetDir(); d != nil {
					// The fixture's rehashed directory hashes are not rebuilt here.
					require.Len(t, d.GetTreeHash(), 32)
					d.TreeHash = nil
				}
			}
			require.True(t, proto.Equal(legacy.parts[i], got), "%s: part %d\nwant %v\ngot  %v", form.name, i, legacy.parts[i], got)
		}

		// Every declined delta request is answered with exactly that.
		declined := map[string]func(req *pb.GetTreeManifestRequest){
			"another view": func(req *pb.GetTreeManifestRequest) {
				req.BaseCommitId, req.BaseView = &f.baseID, []byte("another view")
			},
			"no view":                 func(req *pb.GetTreeManifestRequest) { req.BaseCommitId = &f.baseID },
			"unknown base":            func(req *pb.GetTreeManifestRequest) { req.BaseCommitId, req.BaseView = &unknown, []byte(f.view) },
			"base of another project": func(req *pb.GetTreeManifestRequest) { req.BaseCommitId, req.BaseView = &foreign, []byte(f.view) },
			"invalid base id":         func(req *pb.GetTreeManifestRequest) { req.BaseCommitId, req.BaseView = &bad, []byte(f.view) },
			"another sparse set": func(req *pb.GetTreeManifestRequest) {
				sparse, err := f.branch.ManifestView(context.Background(), 42, []string{"lib"}, true)
				require.NoError(t, err)
				req.BaseCommitId, req.BaseView = &f.baseID, sparse
			},
			"view without a base": func(req *pb.GetTreeManifestRequest) { req.BaseView = []byte(f.view) },
		}
		if !form.req().GetRecursive() {
			// A delta is only defined for recursive manifests.
			declined["the right view"] = func(req *pb.GetTreeManifestRequest) { req.BaseCommitId, req.BaseView = &f.baseID, []byte(f.view) }
		}
		for name, mutate := range declined {
			req := form.req()
			mutate(req)
			got := f.stream(t, req)
			require.Len(t, got, len(plain), "%s, %s", form.name, name)
			for i := range plain {
				require.True(t, proto.Equal(plain[i], got[i]), "%s, %s: part %d", form.name, name, i)
			}
		}
	}

	// What is wrong with the target is reported as before.
	for name, tc := range map[string]struct {
		req  *pb.GetTreeManifestRequest
		code codes.Code
	}{
		"invalid target id": {&pb.GetTreeManifestRequest{CommitId: &bad, Recursive: true}, codes.InvalidArgument},
		"unknown target":    {&pb.GetTreeManifestRequest{CommitId: &unknown, Recursive: true}, codes.NotFound},
		"foreign target":    {&pb.GetTreeManifestRequest{CommitId: &foreign, Recursive: true}, codes.NotFound},
		"unknown branch":    {&pb.GetTreeManifestRequest{Branch: "nope", Recursive: true}, codes.NotFound},
	} {
		f.repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "nope").Return(nil, domain.NewErrorNotFound("branch")).AnyTimes()
		for _, withBase := range []bool{false, true} {
			req := proto.Clone(tc.req).(*pb.GetTreeManifestRequest)
			req.Context = &pb.ProjectContext{Org: "org", Project: "proj"}
			if withBase {
				req.BaseCommitId, req.BaseView = &f.baseID, []byte(f.view)
			}
			stream := &fakeManifestStream{}
			err := f.srv.StreamTreeManifest(req, stream)
			require.Equal(t, tc.code, status.Code(err), "%s (base %t): %v", name, withBase, err)
			require.Empty(t, stream.parts)
		}
	}
}

func TestStreamTreeManifest_DeltaFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	branch := usecase.NewBranch(perm, repo, node)
	srv := New(newMockUsecaseContainer(t, branch))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true).AnyTimes()
	head := snow.ID(9)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").Return(&domain.Branch{ID: 1, ProjectID: 42, Name: "main", CommitID: &head}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), head).Return(&domain.Commit{ID: head, ProjectID: 42, TreeID: 100}, nil).AnyTimes()

	// The rules cannot be read while building the delta: a server error.
	boom := errors.New("rules unavailable")
	perm.EXPECT().CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, boom)
	base := snow.ID(8).Base36()
	stream := &fakeManifestStream{}
	err = srv.StreamTreeManifest(&pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Branch: "main", Recursive: true, BaseCommitId: &base, BaseView: []byte("v"),
	}, stream)
	require.Error(t, err)
	require.Empty(t, stream.parts)

	// A manifest that was built is sent even when its token cannot be.
	perm.EXPECT().CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).Return(usecase.AllowAllFilter(), nil)
	perm.EXPECT().CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, boom)
	expectSmallTree(repo, 100)
	stream = &fakeManifestStream{}
	require.NoError(t, srv.StreamTreeManifest(&pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Branch: "main", Recursive: true,
	}, stream))
	require.Empty(t, stream.parts[0].GetView())
	require.Equal(t, []string{"dir 0 root", "file a.txt 0"}, describe(stream.parts[0].GetEntries()))
}
