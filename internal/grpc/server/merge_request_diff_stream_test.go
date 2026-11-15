package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeDiffStream struct {
	grpc.ServerStream
	parts   []*pb.MergeRequestDiffPart
	sendErr error
}

func (s *fakeDiffStream) Context() context.Context { return context.Background() }

func (s *fakeDiffStream) Send(part *pb.MergeRequestDiffPart) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.parts = append(s.parts, part)
	return nil
}

func textChange(path string, oldText, newText string) diff.FileDiff {
	return diff.FileDiff{
		Change: diff.Change{Path: path, Status: diff.Modified, Old: diff.Entry{Path: path}, New: diff.Entry{Path: path}},
		Old:    []byte(oldText),
		New:    []byte(newText),
	}
}

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%s line %06d with some padding to make it longer\n", prefix, i)
	}
	return b.String()
}

func newDiffStreamServer(t *testing.T, merger *stubBranchMerger) *nipaServer {
	repo := &stubMergeRequestRepository{
		onGet: func(int) (*domain.MergeRequest, error) {
			return testMergeRequest(5, domain.MergeRequestOpen), nil
		},
	}
	srv, branchRepo, perm := newTestMergeRequestServer(t, repo, merger)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 42, CommitID: ptrSnow(11)}, nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 42, CommitID: ptrSnow(12)}, nil).AnyTimes()
	return srv
}

var diffReq = &pb.GetMergeRequestDiffRequest{Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Number: 5}

func TestStreamMergeRequestDiff_SplitsPartsAndFlagsLargeFiles(t *testing.T) {
	var files []diff.FileDiff
	for i := 0; i < 40; i++ {
		files = append(files, textChange(fmt.Sprintf("src/f%02d.txt", i), lines(400, "old"), lines(400, "new")))
	}
	files = append(files, textChange("huge.txt", "", lines(40000, "big")))
	merger := &stubBranchMerger{base: nil, diff: files}
	srv := newDiffStreamServer(t, merger)

	stream := &fakeDiffStream{}
	require.NoError(t, srv.StreamMergeRequestDiff(diffReq, stream))
	require.Greater(t, len(stream.parts), 1)

	var got []*pb.DiffFileDetail
	for _, part := range stream.parts {
		require.LessOrEqual(t, proto.Size(part), diffPartBudget+4096)
		got = append(got, part.GetFiles()...)
	}
	require.Len(t, got, 41)
	for i, f := range got[:40] {
		require.Equal(t, fmt.Sprintf("src/f%02d.txt", i), f.GetPath())
		require.False(t, f.GetTooLarge())
		require.NotEmpty(t, f.GetHunks())
	}
	huge := got[40]
	require.Equal(t, "huge.txt", huge.GetPath())
	require.True(t, huge.GetTooLarge())
	require.Empty(t, huge.GetPatch())
	require.Empty(t, huge.GetHunks())
	require.Equal(t, int64(40000), huge.GetAdditions())
}

func TestStreamMergeRequestDiff_EmptyAndErrors(t *testing.T) {
	t.Run("no files sends nothing", func(t *testing.T) {
		stream := &fakeDiffStream{}
		require.NoError(t, newDiffStreamServer(t, &stubBranchMerger{}).StreamMergeRequestDiff(diffReq, stream))
		require.Empty(t, stream.parts)
	})
	t.Run("unknown project", func(t *testing.T) {
		srv := newDiffStreamServer(t, &stubBranchMerger{})
		err := srv.StreamMergeRequestDiff(&pb.GetMergeRequestDiffRequest{Context: &pb.ProjectContext{Org: "nope", Project: "proj"}, Number: 5}, &fakeDiffStream{})
		require.Error(t, err)
	})
	t.Run("diff error", func(t *testing.T) {
		srv := newDiffStreamServer(t, &stubBranchMerger{diffErr: domain.NewErrorNotFound("gone")})
		err := srv.StreamMergeRequestDiff(diffReq, &fakeDiffStream{})
		require.Equal(t, codes.NotFound, status.Code(err))
	})
	t.Run("send error", func(t *testing.T) {
		boom := errors.New("boom")
		var files []diff.FileDiff
		for i := 0; i < 40; i++ {
			files = append(files, textChange(fmt.Sprintf("f%02d", i), lines(400, "old"), lines(400, "new")))
		}
		srv := newDiffStreamServer(t, &stubBranchMerger{diff: files})
		require.ErrorIs(t, srv.StreamMergeRequestDiff(diffReq, &fakeDiffStream{sendErr: boom}), boom)
	})
	t.Run("final send error", func(t *testing.T) {
		boom := errors.New("boom")
		srv := newDiffStreamServer(t, &stubBranchMerger{diff: []diff.FileDiff{textChange("a", "1\n", "2\n")}})
		require.ErrorIs(t, srv.StreamMergeRequestDiff(diffReq, &fakeDiffStream{sendErr: boom}), boom)
	})
}
