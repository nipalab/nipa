package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type SSHPublicKey struct {
	ID          snow.ID   `json:"id"`
	UserID      snow.ID   `json:"user_id"`
	Name        string    `json:"name"`
	KeyType     string    `json:"key_type"`
	KeyBlob     string    `json:"key_blob"`
	Fingerprint string    `json:"fingerprint"`
	Comment     string    `json:"comment"`
	CreatedAt   time.Time `json:"created_at"`
}
