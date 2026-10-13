package model

import "time"

type SSHKeyResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	KeyType     string    `json:"key_type"`
	Fingerprint string    `json:"fingerprint"`
	Comment     string    `json:"comment,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type AddSSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}
