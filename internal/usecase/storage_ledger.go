package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// LedgerChunk is a chunk attributed to a project together with its
// server-measured stored size.
//
// Chunks belong to one project (per-project dedup), so a project is charged
// for exactly the chunk rows it has: physical bytes are the project's bytes.
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
	// AttributedSizes returns the stored size of every hash the project
	// already has a chunk row for. Other hashes are absent from the result.
	AttributedSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error)

	// Headroom returns a quota error when projectedNewBytes would exceed the
	// remaining storage quota.
	Headroom(ctx context.Context, org *domain.Organization, project *domain.Project, projectedNewBytes int64) error

	// Attribute charges the chunks the project has no chunk row for yet to
	// the usage counters, enforcing the quota, under the owner's lock. The
	// caller inserts those rows right after, in the same transaction (the
	// context carries it), so the rows never become visible before the quota
	// accepted them. When the batch would exceed the quota nothing is recorded
	// and a quota error is returned.
	Attribute(ctx context.Context, org *domain.Organization, project *domain.Project, chunks []LedgerChunk) error

	// AttributeProjectChunks attributes chunks the server produced itself
	// (merge strategies), where only the project id is at hand.
	// Implementations resolve the owning organization.
	AttributeProjectChunks(ctx context.Context, projectID snow.ID, chunks []LedgerChunk) error

	// ReleaseProject gives a deleted project's bytes back to the owner's
	// quota (its chunk rows stay until the purge). It returns the released
	// bytes and is idempotent: releasing an already released project
	// returns 0.
	ReleaseProject(ctx context.Context, projectID snow.ID) (int64, error)

	// RestoreProject charges a restored project's chunk rows back to the
	// owner's quota. It returns the charged bytes and a quota error, recording
	// nothing, when they do not fit.
	RestoreProject(ctx context.Context, projectID snow.ID) (int64, error)
}
