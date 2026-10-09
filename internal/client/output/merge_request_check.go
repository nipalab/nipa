package output

import (
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

// MergeRequestChecks is the JSON envelope for `nipa mr checks --json`.
type MergeRequestChecks struct {
	Checks []MergeRequestCheck `json:"checks"`
}

// MergeRequestCheck is one status check of the current source head.
type MergeRequestCheck struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	State      string                   `json:"state"`
	DetailsURL string                   `json:"details_url,omitempty"`
	Reporter   clientDomain.ReviewActor `json:"reporter"`
	CreatedAt  string                   `json:"created_at,omitempty"`
	UpdatedAt  string                   `json:"updated_at,omitempty"`
}

func NewMergeRequestChecks(checks []*clientDomain.MergeRequestCheck) MergeRequestChecks {
	out := MergeRequestChecks{Checks: make([]MergeRequestCheck, 0, len(checks))}
	for _, check := range checks {
		if check == nil {
			continue
		}
		out.Checks = append(out.Checks, MergeRequestCheck{
			ID:         check.ID,
			Name:       check.Name,
			State:      check.State,
			DetailsURL: check.DetailsURL,
			Reporter:   check.Reporter,
			CreatedAt:  formatTime(check.CreatedAt),
			UpdatedAt:  formatTime(check.UpdatedAt),
		})
	}
	return out
}
