package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// StorageServer is an S3-compatible server a user registered to hold the
// chunk content of their projects. SecretSealed is the sealed secret key.
type StorageServer struct {
	ID           snow.ID
	OwnerUserID  snow.ID
	Name         string
	Endpoint     string
	Region       string
	Bucket       string
	Prefix       string
	AccessKeyID  string
	SecretSealed []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
