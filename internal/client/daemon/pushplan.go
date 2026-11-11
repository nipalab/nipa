package daemon

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/assetscan"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// PushCheckRunner computes the push plan of a clone and its warn-only asset
// checks (budgets, broken references), offline.
type PushCheckRunner interface {
	Plan(ctx context.Context, root string) (*clientDomain.Plan, error)
	Report(ctx context.Context, root string, plan *clientDomain.Plan) *assetscan.PushChecks
}

type planner interface {
	Plan(ctx context.Context, root string) (*clientDomain.Plan, error)
}

type pushChecker struct {
	mu   sync.Mutex
	push planner
	diff *usecase.Diff
}

// NewPushChecker serializes plan and report calls over usecases with their
// own localrepo handles, like SerialDiff.
func NewPushChecker(push planner, diff *usecase.Diff) PushCheckRunner {
	return &pushChecker{push: push, diff: diff}
}

func (p *pushChecker) Plan(ctx context.Context, root string) (*clientDomain.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.push.Plan(ctx, root)
}

func (p *pushChecker) Report(ctx context.Context, root string, plan *clientDomain.Plan) *assetscan.PushChecks {
	p.mu.Lock()
	defer p.mu.Unlock()
	return assetscan.PushReport(ctx, p.diff, root, plan)
}

// PushPlan reports what a push would send plus the asset budget violations
// and reference impact of the staged changes. Check failures are warnings.
func (s *Server) PushPlan(ctx context.Context, req *daemonpb.PushPlanRequest) (*daemonpb.PushPlanResponse, error) {
	var res *daemonpb.PushPlanResponse
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		if rp.pushCheck == nil {
			return status.Error(codes.FailedPrecondition, "push checks are not configured")
		}
		plan, err := rp.pushCheck.Plan(ctx, rp.root)
		if err != nil {
			return err
		}
		res = pushPlanToPB(plan, rp.pushCheck.Report(ctx, rp.root, plan))
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}

func pushPlanToPB(plan *clientDomain.Plan, checks *assetscan.PushChecks) *daemonpb.PushPlanResponse {
	res := &daemonpb.PushPlanResponse{
		UpToDate:        plan.UpToDate,
		UploadObjects:   int32(plan.UploadObjects),
		UploadBytes:     plan.UploadBytes,
		Changes:         []*daemonpb.PushPlanChange{},
		AssetViolations: []*daemonpb.AssetViolation{},
		Warnings:        []string{},
	}
	for _, c := range plan.Changes {
		res.Changes = append(res.Changes, &daemonpb.PushPlanChange{Path: c.Path, Status: c.Status, Binary: c.Binary, SizeBytes: c.SizeBytes})
	}
	if checks == nil {
		return res
	}
	for _, v := range checks.Violations {
		res.AssetViolations = append(res.AssetViolations, &daemonpb.AssetViolation{
			Path: v.Path, Rule: v.Rule, Limit: v.Limit, Actual: anyString(v.Actual), Max: anyString(v.Max), Message: v.Message,
		})
	}
	if checks.Impact != nil {
		impact := &daemonpb.AssetImpact{
			Broken:      []*daemonpb.BrokenReference{},
			NewlyUnused: append([]string{}, checks.Impact.NewlyUnused...),
			Scanned:     int32(checks.Impact.Scanned),
			Truncated:   checks.Impact.Truncated,
		}
		for _, b := range checks.Impact.Broken {
			impact.Broken = append(impact.Broken, &daemonpb.BrokenReference{From: b.From, Raw: b.Raw, Target: b.Target, Reason: b.Reason})
		}
		res.AssetImpact = impact
	}
	if checks.BudgetErr != nil {
		res.Warnings = append(res.Warnings, "budget check skipped: "+checks.BudgetErr.Error())
	}
	if checks.ImpactErr != nil {
		res.Warnings = append(res.Warnings, "impact check skipped: "+checks.ImpactErr.Error())
	}
	return res
}

func anyString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
