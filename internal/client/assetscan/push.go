package assetscan

import (
	"context"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// PushChecks is the warn-only report of a push plan. BudgetErr and ImpactErr
// carry the failures of each check; the other check still ran.
type PushChecks struct {
	Violations []assetbudget.Violation
	Impact     *assetrefs.Impact
	BudgetErr  error
	ImpactErr  error
}

// PlanChanges maps the staged changes of a push plan to snapshot vs working
// copy changes.
func PlanChanges(plan *domain.Plan) *Changes {
	changes := &Changes{Old: SideSnapshot, New: SideWorking}
	for _, ch := range plan.Changes {
		changes.Files = append(changes.Files, Change{
			Item:    ChangeItem(ch.Path, "", ch.Status, SideSnapshot, SideWorking),
			NewSize: ch.SizeBytes,
		})
	}
	return changes
}

// PushReport checks the staged changes of a push plan against the budget and
// for broken asset references, offline. An empty, complete impact is nil.
func PushReport(ctx context.Context, d *usecase.Diff, root string, plan *domain.Plan) *PushChecks {
	return pushReport(ctx, d, root, plan, assetrefs.Options{})
}

func pushReport(ctx context.Context, d *usecase.Diff, root string, plan *domain.Plan, opts assetrefs.Options) *PushChecks {
	out := &PushChecks{}
	if plan == nil || d == nil {
		return out
	}
	changes := PlanChanges(plan)
	out.Violations, out.BudgetErr = pushBudget(ctx, d, root, changes)
	impact, err := Impact(ctx, d, root, changes, opts)
	if err != nil {
		out.ImpactErr = err
	} else if !impact.Empty() || (impact != nil && impact.Truncated) {
		out.Impact = impact
	}
	return out
}

func pushBudget(ctx context.Context, d *usecase.Diff, root string, changes *Changes) ([]assetbudget.Violation, error) {
	cfg, err := assetbudget.Load(root)
	if err != nil || cfg == nil {
		return nil, err
	}
	report, err := CheckChanges(ctx, BlobLoader(d, root), cfg, changes)
	if err != nil {
		return nil, err
	}
	return report.Violations, nil
}
