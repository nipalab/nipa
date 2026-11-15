package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// WaitlistEntry is an address that tried to sign up while registration was
// closed; NotifiedAt is set once it was told it can sign up.
type WaitlistEntry struct {
	ID         snow.ID
	Email      string
	Name       string
	Source     string
	CreatedAt  time.Time
	NotifiedAt *time.Time
}
