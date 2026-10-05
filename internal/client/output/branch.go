package output

import (
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type CurrentBranch struct {
	Branch string `json:"branch"`
}

type Branches struct {
	Current  string   `json:"current"`
	Branches []Branch `json:"branches"`
}

type Branch struct {
	Name              string `json:"name"`
	Current           bool   `json:"current"`
	Default           bool   `json:"default"`
	Protected         bool   `json:"protected"`
	RequiredApprovals int64  `json:"required_approvals,omitempty"`
	CommitID          string `json:"commit_id,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

func NewCurrentBranch(name string) CurrentBranch {
	return CurrentBranch{Branch: name}
}

func NewBranches(current string, branches []*serverDomain.Branch) Branches {
	out := Branches{Current: current, Branches: make([]Branch, 0, len(branches))}
	for _, b := range branches {
		if b == nil {
			continue
		}
		out.Branches = append(out.Branches, Branch{
			Name:              b.Name,
			Current:           b.Name == current,
			Default:           b.IsDefault,
			Protected:         b.IsProtected,
			RequiredApprovals: b.RequiredApprovals,
			CommitID:          branchCommitID(b),
			UpdatedAt:         formatTime(b.UpdatedAt),
		})
	}
	return out
}

func branchCommitID(b *serverDomain.Branch) string {
	if b.CommitID == nil {
		return ""
	}
	return b.CommitID.Base36()
}
