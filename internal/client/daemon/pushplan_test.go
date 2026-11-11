package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/assetscan"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type stubPushCheck struct {
	plan    *clientDomain.Plan
	planErr error
	checks  *assetscan.PushChecks
	roots   []string
}

func (s *stubPushCheck) Plan(_ context.Context, root string) (*clientDomain.Plan, error) {
	s.roots = append(s.roots, root)
	return s.plan, s.planErr
}

func (s *stubPushCheck) Report(_ context.Context, _ string, _ *clientDomain.Plan) *assetscan.PushChecks {
	return s.checks
}

func pushPlanServer(t *testing.T, runner PushCheckRunner) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	ops := RepoOps{}
	if runner != nil {
		ops.PushCheck = runner
	}
	srv := newOpServer(t, ops)
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func TestServer_PushPlan(t *testing.T) {
	stub := &stubPushCheck{
		plan: &clientDomain.Plan{Kind: "push", UploadObjects: 2, UploadBytes: 30, Changes: []clientDomain.PlanChange{
			{Path: "tex.png", Status: "M", Binary: true, SizeBytes: 20},
		}},
		checks: &assetscan.PushChecks{
			Violations: []assetbudget.Violation{{Path: "tex.png", Rule: "textures", Limit: "power_of_two", Actual: "6x4", Max: nil, Message: "6x4 is not a power of two"}},
			Impact: &assetrefs.Impact{
				Broken:      []assetrefs.BrokenRef{{From: "a.mat", Raw: "guid:x", Target: "tex.png", Reason: "removed"}},
				NewlyUnused: []string{"old.png"},
				Scanned:     7,
				Truncated:   true,
			},
			BudgetErr: errors.New("bad yaml"),
			ImpactErr: errors.New("walk failed"),
		},
	}
	srv, root := pushPlanServer(t, stub)
	res, err := srv.PushPlan(context.Background(), &daemonpb.PushPlanRequest{Root: root})
	require.NoError(t, err)
	assert.Equal(t, []string{root}, stub.roots)
	assert.Equal(t, int32(2), res.GetUploadObjects())
	assert.Equal(t, int64(30), res.GetUploadBytes())
	require.Len(t, res.GetChanges(), 1)
	assert.True(t, res.GetChanges()[0].GetBinary())
	require.Len(t, res.GetAssetViolations(), 1)
	v := res.GetAssetViolations()[0]
	assert.Equal(t, "textures", v.GetRule())
	assert.Equal(t, "power_of_two", v.GetLimit())
	assert.Equal(t, "6x4", v.GetActual())
	assert.Empty(t, v.GetMax())
	impact := res.GetAssetImpact()
	require.NotNil(t, impact)
	assert.Equal(t, "a.mat", impact.GetBroken()[0].GetFrom())
	assert.Equal(t, "removed", impact.GetBroken()[0].GetReason())
	assert.Equal(t, []string{"old.png"}, impact.GetNewlyUnused())
	assert.Equal(t, int32(7), impact.GetScanned())
	assert.True(t, impact.GetTruncated())
	assert.Equal(t, []string{"budget check skipped: bad yaml", "impact check skipped: walk failed"}, res.GetWarnings())
}

func TestServer_PushPlanEmptyChecks(t *testing.T) {
	srv, root := pushPlanServer(t, &stubPushCheck{plan: &clientDomain.Plan{Kind: "push", UpToDate: true}})
	res, err := srv.PushPlan(context.Background(), &daemonpb.PushPlanRequest{Root: root})
	require.NoError(t, err)
	assert.True(t, res.GetUpToDate())
	assert.NotNil(t, res.GetChanges())
	assert.NotNil(t, res.GetAssetViolations())
	assert.NotNil(t, res.GetWarnings())
	assert.Nil(t, res.GetAssetImpact())
}

func TestServer_PushPlanErrors(t *testing.T) {
	srv, root := pushPlanServer(t, nil)
	_, err := srv.PushPlan(context.Background(), &daemonpb.PushPlanRequest{Root: root})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	srv, root = pushPlanServer(t, &stubPushCheck{planErr: clientDomain.NewUserError("nothing staged")})
	_, err = srv.PushPlan(context.Background(), &daemonpb.PushPlanRequest{Root: root})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.PushPlan(context.Background(), &daemonpb.PushPlanRequest{Root: t.TempDir()})
	assert.Error(t, err)
}

func TestPushChecker_RealUsecases(t *testing.T) {
	root := newTestClone(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "new.txt"), []byte("hi\n"), 0o644))
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.StageAdd("new.txt"))
	require.NoError(t, lr.Close())
	checker := NewPushChecker(usecase.NewPush(nil, nil, localrepo.NewLocalRepo()), usecase.NewDiff(nil, nil, localrepo.NewLocalRepo()))
	plan, err := checker.Plan(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, plan.Changes, 1)
	checks := checker.Report(context.Background(), root, plan)
	require.NotNil(t, checks)
	assert.Empty(t, checks.Violations)
	assert.Nil(t, checks.Impact)
}
