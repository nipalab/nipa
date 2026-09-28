package output

import clientDomain "github.com/nipalab/nipa/internal/client/domain"

type Status struct {
	Staged    []string `json:"staged"`
	Deleted   []string `json:"deleted"`
	Modified  []string `json:"modified"`
	Untracked []string `json:"untracked"`
	Missing   []string `json:"missing"`
	Conflicts []string `json:"conflicts"`
}

func NewStatus(st *clientDomain.Status) Status {
	return Status{
		Staged:    paths(st.Staged),
		Deleted:   paths(st.Deleted),
		Modified:  paths(st.Modified),
		Untracked: paths(st.Untracked),
		Missing:   paths(st.Missing),
		Conflicts: paths(st.Conflicts),
	}
}

func paths(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
