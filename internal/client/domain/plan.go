package domain

// PlanChange is one file-level change a pending operation would land.
type PlanChange struct {
	Path      string
	Status    string
	Binary    bool
	SizeBytes int64
}

// Plan describes what a push, merge or revert would do without applying it.
type Plan struct {
	Kind          string
	UpToDate      bool
	FastForward   bool
	SourceBranch  string
	Targets       []string
	Conflicts     []string
	Changes       []PlanChange
	UploadObjects int
	UploadBytes   int64
}
