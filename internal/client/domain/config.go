package domain

type Config struct {
	Url    string   `json:"url"`
	Branch string   `json:"branch"`
	Sparse []string `json:"sparse,omitempty"`
}
