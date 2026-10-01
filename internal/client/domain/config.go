package domain

// Head kinds for Config.Head. Only tags are supported for now.
const HeadKindTag = "tag"

// HeadRef records a detached working-copy position: the pinned commit is the
// checkout, the marker only names where it came from.
type HeadRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Config struct {
	Url    string   `json:"url"`
	Branch string   `json:"branch"`
	Sparse []string `json:"sparse,omitempty"`
	Head   *HeadRef `json:"head,omitempty"`
}
