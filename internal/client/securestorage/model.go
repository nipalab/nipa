package securestorage

type AccessToken struct {
	Host         string `json:"host"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Email        string `json:"email,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
}
