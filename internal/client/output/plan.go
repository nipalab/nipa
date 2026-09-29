package output

import clientDomain "github.com/nipalab/nipa/internal/client/domain"

type Plan struct {
	Kind          string       `json:"kind"`
	UpToDate      bool         `json:"up_to_date,omitempty"`
	FastForward   bool         `json:"fast_forward,omitempty"`
	SourceBranch  string       `json:"source_branch,omitempty"`
	Targets       []string     `json:"targets,omitempty"`
	Conflicts     []string     `json:"conflicts"`
	Changes       []PlanChange `json:"changes"`
	UploadObjects int          `json:"upload_objects,omitempty"`
	UploadBytes   int64        `json:"upload_bytes,omitempty"`
}

type PlanChange struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Binary    bool   `json:"binary,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

func NewPlan(p *clientDomain.Plan) Plan {
	out := Plan{Conflicts: []string{}, Changes: []PlanChange{}}
	if p == nil {
		return out
	}
	out.Kind = p.Kind
	out.UpToDate = p.UpToDate
	out.FastForward = p.FastForward
	out.SourceBranch = p.SourceBranch
	out.Targets = p.Targets
	out.UploadObjects = p.UploadObjects
	out.UploadBytes = p.UploadBytes
	if p.Conflicts != nil {
		out.Conflicts = p.Conflicts
	}
	for _, c := range p.Changes {
		out.Changes = append(out.Changes, PlanChange{
			Path:      c.Path,
			Status:    c.Status,
			Binary:    c.Binary,
			SizeBytes: c.SizeBytes,
		})
	}
	return out
}
