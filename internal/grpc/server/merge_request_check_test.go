package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// stubMergeRequestCheckRepository is a hand-rolled repo so the check usecase
// can be exercised through the real gRPC handlers.
type stubMergeRequestCheckRepository struct {
	upsert func(check domain.MergeRequestCheck) (*domain.MergeRequestCheck, error)
	list   func(mergeRequestID int64, head snow.ID) ([]*domain.MergeRequestCheck, error)
}

func (s *stubMergeRequestCheckRepository) Upsert(_ context.Context, check domain.MergeRequestCheck) (*domain.MergeRequestCheck, error) {
	return s.upsert(check)
}

func (s *stubMergeRequestCheckRepository) List(_ context.Context, mergeRequestID int64, head snow.ID) ([]*domain.MergeRequestCheck, error) {
	return s.list(mergeRequestID, head)
}

func newTestMergeRequestCheckUc(t *testing.T, checkRepo *stubMergeRequestCheckRepository, mrRepo *stubMergeRequestRepository) (*usecase.MergeRequestCheck, *MockbranchRepository, *MockpermissionUsecase) {
	t.Helper()
	ctrl := gomock.NewController(t)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	uc := usecase.NewMergeRequestCheck(checkRepo, mrRepo, branchRepo, perm, node)
	return uc, branchRepo, perm
}

func grpcTestMergeRequest(number int64, status string) *domain.MergeRequest {
	return &domain.MergeRequest{
		ID:             number,
		Number:         number,
		ProjectID:      42,
		SourceBranchID: 3,
		TargetBranchID: 2,
		SourceBranch:   "feature",
		TargetBranch:   "main",
		Title:          "Add feature",
		Status:         status,
		CreatedBy:      7,
		CreatedAt:      time.Unix(100, 0),
		UpdatedAt:      time.Unix(200, 0),
	}
}

func TestMergeRequestChecksHandler_Report(t *testing.T) {
	checkRepo := &stubMergeRequestCheckRepository{
		upsert: func(check domain.MergeRequestCheck) (*domain.MergeRequestCheck, error) {
			check.ID = snow.ID(9)
			check.CreatedAt = time.Unix(100, 0)
			check.UpdatedAt = time.Unix(200, 0)
			check.Reporter.Name = "Alice"
			return &check, nil
		},
	}
	mrRepo := &stubMergeRequestRepository{
		onGet: func(int) (*domain.MergeRequest, error) {
			return grpcTestMergeRequest(5, domain.MergeRequestOpen), nil
		},
	}
	uc, branchRepo, perm := newTestMergeRequestCheckUc(t, checkRepo, mrRepo)
	srv := New(&mockUsecaseContainer{common: newTestCommon(), mergeCheck: uc})
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 42, CommitID: &head}, nil)

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	resp, err := srv.ReportMergeRequestCheck(ctx, &pb.ReportMergeRequestCheckRequest{
		Context:    &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:     5,
		Name:       "build",
		State:      domain.MergeRequestCheckSuccess,
		DetailsUrl: "https://ci.example/run/1",
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(9).Base36(), resp.GetCheck().GetId())
	require.Equal(t, "build", resp.GetCheck().GetName())
	require.Equal(t, domain.MergeRequestCheckSuccess, resp.GetCheck().GetState())
	require.Equal(t, "https://ci.example/run/1", resp.GetCheck().GetDetailsUrl())
	require.Equal(t, snow.ID(7).Base36(), resp.GetCheck().GetReporter().GetUserId())
	require.Equal(t, time.Unix(100, 0).UTC(), resp.GetCheck().GetCreatedAt().AsTime())
}

func TestMergeRequestChecksHandler_Report_Unconfigured(t *testing.T) {
	srv := New(&mockUsecaseContainer{common: newTestCommon()})
	_, err := srv.ReportMergeRequestCheck(context.Background(), &pb.ReportMergeRequestCheckRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
		Name:    "build",
		State:   domain.MergeRequestCheckSuccess,
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestMergeRequestChecksHandler_List(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	checkRepo := &stubMergeRequestCheckRepository{
		list: func(mergeRequestID int64, head snow.ID) ([]*domain.MergeRequestCheck, error) {
			require.Equal(t, int64(5), mergeRequestID)
			require.Equal(t, snow.ID(11), head)
			return []*domain.MergeRequestCheck{
				{ID: snow.ID(6), Name: "build", State: domain.MergeRequestCheckFailed, CreatedAt: now, UpdatedAt: now},
				{ID: snow.ID(7), Name: "test", State: domain.MergeRequestCheckSuccess, CreatedAt: now, UpdatedAt: now},
			}, nil
		},
	}
	mrRepo := &stubMergeRequestRepository{
		onGet: func(int) (*domain.MergeRequest, error) {
			return grpcTestMergeRequest(5, domain.MergeRequestOpen), nil
		},
	}
	uc, branchRepo, perm := newTestMergeRequestCheckUc(t, checkRepo, mrRepo)
	srv := New(&mockUsecaseContainer{common: newTestCommon(), mergeCheck: uc})
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 42, CommitID: &head}, nil)

	resp, err := srv.ListMergeRequestChecks(context.Background(), &pb.ListMergeRequestChecksRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.NoError(t, err)
	require.Len(t, resp.GetChecks(), 2)
	require.Equal(t, snow.ID(6).Base36(), resp.GetChecks()[0].GetId())
	require.Equal(t, domain.MergeRequestCheckFailed, resp.GetChecks()[0].GetState())
	require.Equal(t, now, resp.GetChecks()[1].GetCreatedAt().AsTime())
}

func TestMergeRequestChecksHandler_List_Unconfigured(t *testing.T) {
	srv := New(&mockUsecaseContainer{common: newTestCommon()})
	_, err := srv.ListMergeRequestChecks(context.Background(), &pb.ListMergeRequestChecksRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Number:  5,
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestMergeRequestChecksHandler_MissingProject(t *testing.T) {
	srv := New(&mockUsecaseContainer{common: newTestCommon()})
	_, err := srv.ReportMergeRequestCheck(context.Background(), &pb.ReportMergeRequestCheckRequest{
		Context: &pb.ProjectContext{Org: "nope", Project: "proj"},
		Number:  5,
		Name:    "build",
		State:   domain.MergeRequestCheckSuccess,
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestDomainMergeRequestCheckToPB(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	in := &domain.MergeRequestCheck{
		ID:         snow.ID(9),
		Name:       "build",
		State:      domain.MergeRequestCheckSuccess,
		DetailsURL: "https://ci.example/run/1",
		Reporter:   domain.ReviewActor{UserID: snow.ID(7), Name: "Alice", PhotoURL: "https://x/a.png"},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	out := domainMergeRequestCheckToPB(in)
	require.Equal(t, snow.ID(9).Base36(), out.Id)
	require.Equal(t, in.Name, out.Name)
	require.Equal(t, in.State, out.State)
	require.Equal(t, in.DetailsURL, out.DetailsUrl)
	require.Equal(t, snow.ID(7).Base36(), out.Reporter.UserId)
	require.Equal(t, "Alice", out.Reporter.Name)
	require.Equal(t, "https://x/a.png", out.Reporter.PhotoUrl)
	require.Equal(t, now, out.CreatedAt.AsTime())
	require.Equal(t, now, out.UpdatedAt.AsTime())

	require.Nil(t, domainMergeRequestCheckToPB(nil))

	empty := domainMergeRequestCheckToPB(&domain.MergeRequestCheck{Reporter: domain.ReviewActor{UserID: snow.ID(8)}})
	require.Equal(t, snow.ID(8).Base36(), empty.Reporter.UserId)
}
