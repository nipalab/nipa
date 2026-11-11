package service

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type BudgetViolation struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Limit   string `json:"limit"`
	Actual  string `json:"actual"`
	Max     string `json:"max"`
	Message string `json:"message"`
}

type BrokenReference struct {
	From   string `json:"from"`
	Raw    string `json:"raw"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

type PushChecks struct {
	Violations  []BudgetViolation `json:"violations"`
	Broken      []BrokenReference `json:"broken"`
	NewlyUnused []string          `json:"newlyUnused"`
	Truncated   bool              `json:"truncated"`
	Scanned     int               `json:"scanned"`
	Warnings    []string          `json:"warnings"`
}

// PushChecks reports the asset budget violations and reference impact of the
// staged changes. They only warn; pushing stays allowed.
func (s *Service) PushChecks(ctx context.Context, root string) (PushChecks, error) {
	out := PushChecks{Violations: []BudgetViolation{}, Broken: []BrokenReference{}, NewlyUnused: []string{}, Warnings: []string{}}
	client, err := s.client(ctx, root)
	if err != nil {
		return out, err
	}
	resp, err := client.PushPlan(ctx, &daemonpb.PushPlanRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return out, cleanError(err)
	}
	for _, v := range resp.GetAssetViolations() {
		out.Violations = append(out.Violations, BudgetViolation{
			Path: v.GetPath(), Rule: v.GetRule(), Limit: v.GetLimit(), Actual: v.GetActual(), Max: v.GetMax(), Message: v.GetMessage(),
		})
	}
	if impact := resp.GetAssetImpact(); impact != nil {
		for _, b := range impact.GetBroken() {
			out.Broken = append(out.Broken, BrokenReference{From: b.GetFrom(), Raw: b.GetRaw(), Target: b.GetTarget(), Reason: b.GetReason()})
		}
		out.NewlyUnused = append(out.NewlyUnused, impact.GetNewlyUnused()...)
		out.Truncated = impact.GetTruncated()
		out.Scanned = int(impact.GetScanned())
	}
	out.Warnings = append(out.Warnings, resp.GetWarnings()...)
	return out, nil
}
