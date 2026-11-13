package dto

type LoginResult struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"-"`
	// MFAChallenge is set instead of the tokens when the account must still
	// present a second factor.
	MFAChallenge *MFAChallenge `json:"-"`
}

type MFAChallenge struct {
	Token     string
	ExpiresIn int
}

type SSHKeyChallenge struct {
	Nonce     string `json:"nonce"`
	Audience  string `json:"audience"`
	ExpiresIn int    `json:"expires_in"`
}
