package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// LedgerChunk is a chunk attributed to a project together with its
// server-measured stored size.
type LedgerChunk struct {
	Hash      domain.Hash
	SizeBytes int64
}

// StorageLedger attributes chunk bytes to projects and enforces the storage
// quota of the organization's owner. A nil ledger disables tracking entirely:
// no quota applies and nothing is recorded.
//
//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=storage_ledger_mock_test.go -package=usecase
type StorageLedger interface {
	// AttributedSizes returns the stored size of every hash already linked to
	// the project. Hashes without a link are absent from the result.
	AttributedSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error)

	// Headroom returns a quota error when projectedNewBytes would exceed the
	// remaining storage quota.
	Headroom(ctx context.Context, org *domain.Organization, project *domain.Project, projectedNewBytes int64) error

	// Attribute links the chunks to the project and updates the usage counters
	// in one step, enforcing the quota. When the batch would exceed the quota
	// nothing is recorded and a quota error is returned.
	Attribute(ctx context.Context, org *domain.Organization, project *domain.Project, chunks []LedgerChunk) error
}
