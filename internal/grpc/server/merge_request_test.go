package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type stubMergeRequestRepository struct {
	created       *domain.MergeRequest
	list          []*domain.MergeRequest
	listStatus    string
	listLimit     int
	getCalls      int
	onGet         func(call int) (*domain.MergeRequest, error)
	updateFn      func(id int64, title, description string) (*domain.MergeRequest, error)
	status        string
	mergeCommitID *snow.ID
}

func (s *stubMergeRequestRepository) Create(_ context.Context, mr domain.MergeRequest) (*domain.MergeRequest, error) {
	mr.CreatedAt = time.Unix(100, 0)
	mr.UpdatedAt = time.Unix(200, 0)
	s.created = &mr
	return &mr, nil
}

func (s *stubMergeRequestRepository) Get(_ context.Context, _ snow.ID, _ int64) (*domain.MergeRequest, error) {
	if s.onGet == nil {
		return nil, domain.NewErrorRecordNotFound()
	}
	s.getCalls++
	return s.onGet(s.getCalls)
}

func (s *stubMergeRequestRepository) List(_ context.Context, _ snow.ID, status string, limit int) ([]*domain.MergeRequest, error) {
	s.listStatus = status
	s.listLimit = limit
	return s.list, nil
}

func (s *stubMergeRequestRepository) Update(_ context.Context, _ snow.ID, id int64, title, description string) (*domain.MergeRequest, error) {
	if s.updateFn == nil {
		return nil, domain.NewErrorRecordNotFound()
	}
	return s.updateFn(id, title, description)
}

func (s *stubMergeRequestRepository) UpdateStatus(_ context.Context, _ snow.ID, _ int64, status string, mergeCommitID *snow.ID) error {
	s.status = status
	s.mergeCommitID = mergeCommitID
	return nil
}

type stubBranchMerger struct {
	base     *usecase.MergeBaseInfo
	baseErr  error
	ffBranch *domain.Branch
	ffErr    error
}

func (s *stubBranchMerger) GetMergeBase(_ context.Context, _ snow.ID, _, _ usecase.MergeRef) (*usecase.MergeBaseInfo, error) {
	if s.baseErr != nil {
		return nil, s.baseErr
	}
	if s.base != nil {
		return s.base, nil
	}
	return &usecase.MergeBaseInfo{}, nil
}

func (s *stubBranchMerger) FastForwardForMergeRequest(_ context.Context, _ snow.ID, _, _ string) (*domain.Branch, error) {
	if s.ffErr != nil {
		return nil, s.ffErr
	}
	return s.ffBranch, nil
}

func (s *stubBranchMerger) TreeDiffBetween(_ context.Context, _ snow.ID, _ *snow.ID, _ snow.ID) ([]diff.FileDiff, error) {
	return nil, nil
}

func (s *stubBranchMerger) BinaryChangesBetween(_ context.Context, _ snow.ID, _, _ *snow.ID) ([]string, error) {
	return nil, nil
}

func testMergeRequest(id int64, status string) *domain.MergeRequest {
	return &domain.MergeRequest{
		ID:             id,
		Number:         id,
		ProjectID:      42,
		SourceBranchID: 3,
		TargetBranchID: 2,
		SourceBranch:   "feature",
		TargetBranch:   "main",
		Title:          "Add feature",
		Description:    "body",
		Status:         status,
		CreatedBy:      7,
		CreatedAt:      time.Unix(100, 0),
		UpdatedAt:      time.Unix(200, 0),
	}
}

func newTestMergeRequestServer(t *testing.T, repo *stubMergeRequestRepository, merger *stubBranchMerger) (*nipaServer, *MockbranchRepository, *MockpermissionUsecase) {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	uc := usecase.NewMergeRequest(repo, branchRepo, perm, merger, node)
	return New(&mockUsecaseContainer{common: newTestCommon(), mergeRequest: uc}), branchRepo, perm
}

func TestMergeRequestHandler_Create(t *testing.T) {
	repo := &stubMergeRequestRepository{}
	merger := &stubBranchMerger{base: &usecase.MergeBaseInfo{MergeBaseCommitID: ptrSnow(12)}}
	srv, branchRepo, perm := newTestMergeRequestServer(t, repo, merger)
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 42, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 42, CommitID: &targetHead}, nil)

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	resp, err := srv.CreateMergeRequest(ctx, &pb.CreateMergeRequestRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		Title:        " Add feature ",
		Description:  " body ",
		SourceBranch: "feature",
		TargetBranch: "main",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.MergeRequest.Id)
	require.Equal(t, snow.ID(42).Base36(), resp.MergeRequest.ProjectId)
	require.Equal(t, "Add feature", resp.MergeRequest.Title)
	require.Equal(t, "body", resp.MergeRequest.Description)
	require.Equal(t, domain.MergeRequestOpen, resp.MergeRequest.Status)
	require.Equal(t, snow.ID(7).Base36(), resp.MergeRequest.CreatedBy)
	require.Equal(t, snow.ID(12).Base36(), resp.MergeRequest.GetMergeBaseCommitId())
	require.Equal(t, time.Unix(100, 0).UTC(), resp.MergeRequest.CreatedAt.AsTime())
	require.NotNil(t, repo.created)
	require.Equal(t, snow.ID(3), repo.created.SourceBranchID)
	require.Equal(t, snow.ID(2), repo.created.TargetBranchID)
}

func TestMergeRequestHandler_CreateValidation(t *testing.T) {
	repo := &stubMergeRequestRepository{}
	srv, _, perm := newTestMergeRequestServer(t, repo, &stubBranchMerger{})
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true).AnyTimes()
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})

	_, err := srv.CreateMergeRequest(ctx, &pb.CreateMergeRequestRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		Title:        "  ",
		SourceBranch: "feature",
		TargetBranch: "main",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestMergeRequestHandler_Update(t *testing.T) {
	repo := &stubMergeRequestRepository{
		onGet: func(int) (*domain.MergeRequest, error) {
			return testMergeRequest(5, domain.MergeRequestOpen), nil
		},
		updateFn: func(_ int64, title, description string) (*domain.MergeRequest, error) {
			mr := testMergeRequest(5, domain.MergeRequestOpen)
			mr.Title, mr.Description = title, description
			return mr, nil
		},
	}
	srv, _, perm := newTestMergeRequestServer(t, repo, &stubBranchMerger{})
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true).AnyTimes()
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})

	resp, err := srv.UpdateMergeRequest(ctx, &pb.UpdateMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
		Title:   " Renamed ",
	})
	require.NoError(t, err)
	require.Equal(t, "Renamed", resp.MergeRequest.Title)
	require.Equal(t, "body", resp.MergeRequest.Description)

	_, err = srv.UpdateMergeRequest(ctx, &pb.UpdateMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.UpdateMergeRequest(ctx, &pb.UpdateMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  0,
		Title:   "x",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestMergeRequestHandler_UpdateNotFound(t *testing.T) {
	srv, _, perm := newTestMergeRequestServer(t, &stubMergeRequestRepository{}, &stubBranchMerger{})
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})

	_, err := srv.UpdateMergeRequest(ctx, &pb.UpdateMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  404,
		Title:   "x",
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestMergeRequestHandler_List(t *testing.T) {
	repo := &stubMergeRequestRepository{list: []*domain.MergeRequest{
		testMergeRequest(5, domain.MergeRequestOpen),
		testMergeRequest(4, domain.MergeRequestClosed),
	}}
	srv, _, perm := newTestMergeRequestServer(t, repo, &stubBranchMerger{})
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)

	resp, err := srv.ListMergeRequests(context.Background(), &pb.ListMergeRequestsRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Status:  domain.MergeRequestClosed,
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, resp.MergeRequests, 2)
	require.Equal(t, snow.ID(5).Base36(), resp.MergeRequests[0].Id)
	require.Equal(t, domain.MergeRequestClosed, repo.listStatus)
	require.Equal(t, 10, repo.listLimit)
}

func TestMergeRequestHandler_Merge(t *testing.T) {
	sourceHead, targetHead := snow.ID(11), snow.ID(12)
	repo := &stubMergeRequestRepository{
		onGet: func(call int) (*domain.MergeRequest, error) {
			mr := testMergeRequest(5, domain.MergeRequestOpen)
			if call > 1 {
				mr.Status = domain.MergeRequestMerged
				mr.MergeCommitID = &sourceHead
			}
			return mr, nil
		},
	}
	merger := &stubBranchMerger{
		base:     &usecase.MergeBaseInfo{MergeBaseCommitID: &targetHead, SourceCommitID: &sourceHead, TargetCommitID: &targetHead},
		ffBranch: &domain.Branch{ID: 2, ProjectID: 42, Name: "main", CommitID: &sourceHead},
	}
	srv, branchRepo, perm := newTestMergeRequestServer(t, repo, merger)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 42, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 42, CommitID: &targetHead}, nil)

	resp, err := srv.MergeMergeRequest(context.Background(), &pb.MergeMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestMerged, resp.MergeRequest.Status)
	require.Equal(t, sourceHead.Base36(), resp.MergeRequest.GetMergeCommitId())
	require.Equal(t, domain.MergeabilityMergeable, resp.Mergeability.Status)
	require.Equal(t, domain.MergeRequestMerged, repo.status)
	require.Equal(t, &sourceHead, repo.mergeCommitID)
}

func TestMergeRequestHandler_MergeBehind(t *testing.T) {
	sourceHead, targetHead, base := snow.ID(11), snow.ID(12), snow.ID(9)
	repo := &stubMergeRequestRepository{
		onGet: func(int) (*domain.MergeRequest, error) {
			return testMergeRequest(5, domain.MergeRequestOpen), nil
		},
	}
	merger := &stubBranchMerger{base: &usecase.MergeBaseInfo{MergeBaseCommitID: &base}}
	srv, branchRepo, perm := newTestMergeRequestServer(t, repo, merger)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 42, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 42, CommitID: &targetHead}, nil)

	_, err := srv.MergeMergeRequest(context.Background(), &pb.MergeMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestMergeRequestHandler_Close(t *testing.T) {
	repo := &stubMergeRequestRepository{
		onGet: func(call int) (*domain.MergeRequest, error) {
			mr := testMergeRequest(5, domain.MergeRequestOpen)
			if call > 1 {
				mr.Status = domain.MergeRequestClosed
			}
			return mr, nil
		},
	}
	srv, _, perm := newTestMergeRequestServer(t, repo, &stubBranchMerger{})
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})

	resp, err := srv.CloseMergeRequest(ctx, &pb.CloseMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, resp.MergeRequest.Status)
	require.Equal(t, domain.MergeRequestClosed, repo.status)
}

func TestMergeRequestHandler_CloseDenied(t *testing.T) {
	repo := &stubMergeRequestRepository{
		onGet: func(int) (*domain.MergeRequest, error) {
			return testMergeRequest(5, domain.MergeRequestOpen), nil
		},
	}
	srv, _, perm := newTestMergeRequestServer(t, repo, &stubBranchMerger{})
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(42)).Return(false)
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(99)})

	_, err := srv.CloseMergeRequest(ctx, &pb.CloseMergeRequestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
