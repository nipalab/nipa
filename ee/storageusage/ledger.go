// Package storageusage implements the enterprise storage ledger: per-owner
// quotas and per-project chunk attribution over postgres.
//
// Enterprise Edition: see ee/LICENSE.
package storageusage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// Config tunes the storage ledger.
type Config struct {
	// DefaultQuotaBytes is the quota applied to an owner without an explicit
	// quota row. Zero means unlimited.
	DefaultQuotaBytes int64
}

// Ledger attributes chunk bytes to projects and enforces the storage quota of
// the organization's owner. It is safe for concurrent use: every mutating
// operation runs in one transaction and the counters are updated in the same
// transaction as the chunk links.
type Ledger struct {
	queries      *sqlcPostgres.Queries
	transactor   *dbtx.Transactor
	defaultQuota int64
}

// NewLedger builds a ledger over the postgres database.
func NewLedger(db *sql.DB, cfg Config) *Ledger {
	return &Ledger{
		queries:      sqlcPostgres.New(dbtx.New(db)),
		transactor:   dbtx.NewTransactor(db),
		defaultQuota: cfg.DefaultQuotaBytes,
	}
}

// ProjectUsage is one project's slice of an organization's storage usage.
type ProjectUsage struct {
	ProjectID snow.ID
	Slug      string
	Name      string
	Deleted   bool
	UsedBytes int64
}

// OrgUsage is one organization's storage usage.
type OrgUsage struct {
	OrgID     snow.ID
	Slug      string
	Name      string
	UsedBytes int64
}

// OrgStorage is the storage summary of one organization.
type OrgStorage struct {
	UsedBytes  int64
	QuotaBytes *int64
	OwnerID    snow.ID
	Projects   []ProjectUsage
}

// OwnerStorage is the aggregate storage summary of one billing owner.
type OwnerStorage struct {
	UserID     snow.ID
	UsedBytes  int64
	QuotaBytes *int64
	Orgs       []OrgUsage
}

// AttributedSizes returns the stored size of every hash already linked to the
// project. Hashes without a link are absent from the result. The lookup is one
// batched query: presign hands over the whole unpaginated ref list of a push.
func (l *Ledger) AttributedSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error) {
	sizes := make(map[domain.Hash]int64, len(hashes))
	if len(hashes) == 0 {
		return sizes, nil
	}
	raw := make([][]byte, 0, len(hashes))
	for _, hash := range hashes {
		raw = append(raw, append([]byte(nil), hash[:]...))
	}
	rows, err := l.queries.GetAttributedSizes(ctx, sqlcPostgres.GetAttributedSizesParams{
		ProjectID: projectID.Int64(),
		Hashes:    raw,
	})
	if err != nil {
		return nil, dbError(err)
	}
	for _, row := range rows {
		var hash domain.Hash
		copy(hash[:], row.Hash)
		sizes[hash] = row.SizeBytes
	}
	return sizes, nil
}

// Headroom rejects the projected new bytes when they would exceed the owner's
// remaining quota. It is the advisory early gate: Attribute is authoritative.
func (l *Ledger) Headroom(ctx context.Context, org *domain.Organization, project *domain.Project, projectedNewBytes int64) error {
	if projectedNewBytes <= 0 {
		return nil
	}
	owner, quota, err := l.ownerQuota(ctx, org.ID)
	if err != nil {
		return err
	}
	if quota <= 0 {
		return nil
	}
	used, err := l.queries.GetOwnerUsage(ctx, owner.Int64())
	if err != nil {
		return dbError(err)
	}
	if used+projectedNewBytes > quota {
		return quotaExceeded(used, quota, used+projectedNewBytes)
	}
	return nil
}

// Attribute links the chunks to the project and updates the usage counters in
// one transaction, enforcing the quota. When the batch would exceed the quota
// nothing is recorded and a quota error is returned.
func (l *Ledger) Attribute(ctx context.Context, org *domain.Organization, project *domain.Project, chunks []usecase.LedgerChunk) error {
	return l.attribute(ctx, org.ID, project.ID, chunks)
}

// AttributeProjectChunks attributes server-generated chunks (merge output) to
// the project's owner.
func (l *Ledger) AttributeProjectChunks(ctx context.Context, projectID snow.ID, chunks []usecase.LedgerChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	orgID, err := l.queries.GetProjectOrgID(ctx, projectID.Int64())
	if err != nil {
		return dbError(err)
	}
	return l.attribute(ctx, snow.ID(orgID), projectID, chunks)
}

func (l *Ledger) attribute(ctx context.Context, orgID, projectID snow.ID, chunks []usecase.LedgerChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	return l.transactor.WithinTx(ctx, func(ctx context.Context) error {
		owner, quota, err := l.ownerQuota(ctx, orgID)
		if err != nil {
			return err
		}
		// Serialize quota accounting per owner: without this, two concurrent
		// confirms for the same owner can both read the same used bytes and
		// both pass the quota check at read-committed isolation.
		if err := l.queries.LockBillingOwner(ctx, owner.Int64()); err != nil {
			return dbError(err)
		}
		var used int64
		if quota > 0 {
			used, err = l.queries.GetOwnerUsage(ctx, owner.Int64())
			if err != nil {
				return dbError(err)
			}
		}
		// One batched lookup and one batched link insert keep the per-owner
		// lock short: a confirm can carry hundreds of chunks.
		hashes := make([][]byte, 0, len(chunks))
		for _, chunk := range chunks {
			hashes = append(hashes, append([]byte(nil), chunk.Hash[:]...))
		}
		rows, err := l.queries.GetChunkIDsByHashes(ctx, hashes)
		if err != nil {
			return dbError(err)
		}
		ids := make(map[domain.Hash]int64, len(rows))
		for _, row := range rows {
			var hash domain.Hash
			copy(hash[:], row.Hash)
			ids[hash] = row.ID
		}
		chunkIDs := make([]int64, 0, len(chunks))
		for _, chunk := range chunks {
			id, ok := ids[chunk.Hash]
			if !ok {
				return domain.NewErrorInternalServer(fmt.Sprintf("chunk %s has no metadata row", chunk.Hash))
			}
			chunkIDs = append(chunkIDs, id)
		}
		inserted, err := l.queries.InsertProjectChunks(ctx, sqlcPostgres.InsertProjectChunksParams{
			ProjectID: projectID.Int64(),
			ChunkIds:  chunkIDs,
		})
		if err != nil {
			return dbError(err)
		}
		var newBytes int64
		for _, size := range inserted {
			newBytes += size
		}
		if newBytes > 0 && quota > 0 && used+newBytes > quota {
			return quotaExceeded(used, quota, used+newBytes)
		}
		if newBytes > 0 {
			if err := l.queries.AddProjectUsage(ctx, sqlcPostgres.AddProjectUsageParams{
				ProjectID: projectID.Int64(),
				Delta:     newBytes,
			}); err != nil {
				return dbError(err)
			}
			if err := l.queries.AddOrgUsage(ctx, sqlcPostgres.AddOrgUsageParams{
				OrgID: orgID.Int64(),
				Delta: newBytes,
			}); err != nil {
				return dbError(err)
			}
		}
		return nil
	})
}

// OrgStorage reports one organization's usage, its owner's quota and the
// per-project breakdown.
func (l *Ledger) OrgStorage(ctx context.Context, orgID snow.ID) (*OrgStorage, error) {
	owner, quota, err := l.ownerQuota(ctx, orgID)
	if err != nil {
		return nil, err
	}
	used, err := l.queries.GetOrgUsage(ctx, orgID.Int64())
	if err != nil {
		return nil, dbError(err)
	}
	rows, err := l.queries.ListProjectUsage(ctx, orgID.Int64())
	if err != nil {
		return nil, dbError(err)
	}
	projects := make([]ProjectUsage, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, ProjectUsage{
			ProjectID: snow.ID(row.ProjectID),
			Slug:      row.Slug,
			Name:      row.Name,
			Deleted:   row.Deleted,
			UsedBytes: row.UsedBytes,
		})
	}
	return &OrgStorage{
		UsedBytes:  used,
		QuotaBytes: quotaPtr(quota),
		OwnerID:    owner,
		Projects:   projects,
	}, nil
}

// OwnerStorage reports one owner's aggregate usage, quota and per-org
// breakdown.
func (l *Ledger) OwnerStorage(ctx context.Context, userID snow.ID) (*OwnerStorage, error) {
	quota, err := l.ownerQuotaBytes(ctx, userID)
	if err != nil {
		return nil, err
	}
	used, err := l.queries.GetOwnerUsage(ctx, userID.Int64())
	if err != nil {
		return nil, dbError(err)
	}
	rows, err := l.queries.ListOwnerOrgUsage(ctx, userID.Int64())
	if err != nil {
		return nil, dbError(err)
	}
	orgs := make([]OrgUsage, 0, len(rows))
	for _, row := range rows {
		orgs = append(orgs, OrgUsage{
			OrgID:     snow.ID(row.OrgID),
			Slug:      row.Slug,
			Name:      row.Name,
			UsedBytes: row.UsedBytes,
		})
	}
	return &OwnerStorage{
		UserID:     userID,
		UsedBytes:  used,
		QuotaBytes: quotaPtr(quota),
		Orgs:       orgs,
	}, nil
}

// SetOwnerQuota sets or clears an owner's quota. A nil quota means unlimited.
func (l *Ledger) SetOwnerQuota(ctx context.Context, userID snow.ID, quota *int64) error {
	if quota != nil && *quota < 0 {
		return domain.NewErrorUser("quota_bytes must be zero or positive")
	}
	if err := l.ensureActiveUser(ctx, userID); err != nil {
		return err
	}
	return dbError(l.queries.SetOwnerQuota(ctx, sqlcPostgres.SetOwnerQuotaParams{
		UserID:     userID.Int64(),
		QuotaBytes: nullQuota(quota),
	}))
}

// SetBillingOwner transfers an organization to another billing owner.
func (l *Ledger) SetBillingOwner(ctx context.Context, orgID, userID snow.ID) error {
	if err := l.ensureActiveUser(ctx, userID); err != nil {
		return err
	}
	return dbError(l.queries.UpsertBillingOwner(ctx, sqlcPostgres.UpsertBillingOwnerParams{
		OrgID:  orgID.Int64(),
		UserID: userID.Int64(),
	}))
}

// ensureActiveUser rejects unknown or deleted users with a 404 instead of
// letting the foreign key fail as a generic database error.
func (l *Ledger) ensureActiveUser(ctx context.Context, userID snow.ID) error {
	if _, err := l.queries.GetActiveUserID(ctx, userID.Int64()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewErrorNotFound(fmt.Sprintf("user %s not found", userID.Base36()))
		}
		return dbError(err)
	}
	return nil
}

// ownerQuotaBytes resolves an owner's effective quota; zero means unlimited.
func (l *Ledger) ownerQuotaBytes(ctx context.Context, userID snow.ID) (int64, error) {
	row, err := l.queries.GetOwnerQuota(ctx, userID.Int64())
	switch {
	case err == nil && row.Valid:
		return row.Int64, nil
	case err == nil:
		return 0, nil
	case errors.Is(err, sql.ErrNoRows):
		return l.defaultQuota, nil
	default:
		return 0, dbError(err)
	}
}

func quotaPtr(quota int64) *int64 {
	if quota <= 0 {
		return nil
	}
	return &quota
}

func nullQuota(quota *int64) sql.NullInt64 {
	if quota == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *quota, Valid: true}
}

func quotaExceeded(used, quota, needed int64) error {
	return domain.NewErrorQuotaExceeded(fmt.Sprintf(
		"storage quota exceeded: %d bytes used, %d bytes quota, %d bytes needed", used, quota, needed))
}

func dbError(err error) error {
	if err == nil {
		return nil
	}
	return domain.NewErrorDatabase(err.Error())
}
