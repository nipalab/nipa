package output

import clientDomain "github.com/nipalab/nipa/internal/client/domain"

type Status struct {
	Branch    string      `json:"branch,omitempty"`
	Head      *StatusHead `json:"head,omitempty"`
	Staged    []string    `json:"staged"`
	Deleted   []string    `json:"deleted"`
	Modified  []string    `json:"modified"`
	Untracked []string    `json:"untracked"`
	Missing   []string    `json:"missing"`
	Conflicts []string    `json:"conflicts"`
}

type StatusHead struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func NewStatus(st *clientDomain.Status) Status {
	out := Status{
		Branch:    st.Branch,
		Staged:    paths(st.Staged),
		Deleted:   paths(st.Deleted),
		Modified:  paths(st.Modified),
		Untracked: paths(st.Untracked),
		Missing:   paths(st.Missing),
		Conflicts: paths(st.Conflicts),
	}
	if st.Head != nil {
		out.Head = &StatusHead{Kind: st.Head.Kind, Name: st.Head.Name}
	}
	return out
}

func paths(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
