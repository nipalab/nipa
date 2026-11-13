package domain

type LoginResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Host         string `json:"host"`
	Email        string `json:"email,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	MFAChallenge string `json:"-"`
}

type MFAChallenge struct {
	Challenge string `json:"challenge"`
	ExpiresIn int    `json:"expires_in"`
}

type SSHKeyChallenge struct {
	Nonce     string `json:"nonce"`
	Audience  string `json:"audience"`
	ExpiresIn int    `json:"expires_in"`
}
