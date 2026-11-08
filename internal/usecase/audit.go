package usecase

import (
	"context"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultAuditPageSize = 50
	maxAuditPageSize     = 200
)

// AuditRecorder writes one audit entry. Usecases call it inside the
// transaction of the write it describes, so a failed record rolls the write
// back.
type AuditRecorder interface {
	Record(ctx context.Context, entry domain.AuditEntry) error
}

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=audit_mock_test.go -package=usecase
type auditRepository interface {
	Insert(ctx context.Context, entry domain.AuditEntry) error
	List(ctx context.Context, filter domain.AuditFilter) ([]*domain.AuditEntry, error)
}

type auditProjectOrgs interface {
	ProjectOrgID(ctx context.Context, projectID snow.ID) (snow.ID, error)
}

// Audit records and lists audit log entries.
type Audit struct {
	repo     auditRepository
	node     snow.Node
	tx       transactor
	projects auditProjectOrgs
	now      func() time.Time
}

func NewAudit(repo auditRepository, node snow.Node, tx transactor) *Audit {
	return &Audit{repo: repo, node: node, tx: tx, now: time.Now}
}

// WithProjectOrgs fills the organization of project-scoped entries.
func (a *Audit) WithProjectOrgs(projects auditProjectOrgs) *Audit {
	a.projects = projects
	return a
}

// WithinTx runs fn in the transaction the audited write and its record share.
func (a *Audit) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if a.tx == nil {
		return fn(ctx)
	}
	return a.tx.WithinTx(ctx, fn)
}

// Record completes the entry from the request context and stores it. An entry
// without an actor type is attributed to the authenticated caller, or to the
// system when there is none.
func (a *Audit) Record(ctx context.Context, entry domain.AuditEntry) error {
	if entry.ActorType == "" {
		if claim, ok := domain.ClaimFromContext(ctx); ok {
			entry.ActorType = domain.AuditActorUser
			entry.ActorUserID = domain.IDPtr(claim.UserID)
			if claim.ServiceTokenID != 0 {
				entry.ActorLabel = "service_token:" + claim.ServiceTokenID.Base36()
				if entry.Metadata == nil {
					entry.Metadata = map[string]any{}
				}
				entry.Metadata["service_token_id"] = claim.ServiceTokenID.Base36()
			}
		} else {
			entry.ActorType = domain.AuditActorSystem
		}
	}
	if entry.RequestID == "" {
		entry.RequestID = obs.RequestIDFromContext(ctx)
	}
	if meta, ok := domain.RequestMetaFromContext(ctx); ok {
		if entry.ClientIP == "" {
			entry.ClientIP = meta.ClientIP
		}
		if entry.UserAgent == "" {
			entry.UserAgent = meta.UserAgent
		}
	}
	if entry.OrgID == nil && entry.ProjectID != nil && a.projects != nil {
		orgID, err := a.projects.ProjectOrgID(ctx, *entry.ProjectID)
		if err != nil && !domain.IsErrorNotFound(err) {
			return err
		}
		if err == nil {
			entry.OrgID = domain.IDPtr(orgID)
		}
	}
	if entry.Metadata == nil {
		entry.Metadata = map[string]any{}
	}
	entry.ID = a.node.Generate()
	entry.OccurredAt = a.now().UTC()
	return a.repo.Insert(ctx, entry)
}

// AuditListOptions filters the audit log listing.
type AuditListOptions struct {
	Actor        *snow.ID
	Org          *snow.ID
	Project      *snow.ID
	ActionPrefix string
	Target       string
	Since        *time.Time
	Until        *time.Time
	After        *snow.ID
	Limit        int64
}

// List returns a page of entries, newest first, with the keyset cursor of the
// next page when one exists. Super admins only.
func (a *Audit) List(ctx context.Context, opts AuditListOptions) ([]*domain.AuditEntry, string, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		return nil, "", domain.NewErrorNoPermission()
	}
	if opts.Since != nil && opts.Until != nil && !opts.Until.After(*opts.Since) {
		return nil, "", domain.NewErrorUser("until must be after since")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultAuditPageSize
	}
	if limit > maxAuditPageSize {
		limit = maxAuditPageSize
	}
	entries, err := a.repo.List(ctx, domain.AuditFilter{
		ActorUserID:  opts.Actor,
		OrgID:        opts.Org,
		ProjectID:    opts.Project,
		ActionPrefix: strings.ToLower(strings.TrimSpace(opts.ActionPrefix)),
		TargetID:     strings.TrimSpace(opts.Target),
		Since:        opts.Since,
		Until:        opts.Until,
		After:        opts.After,
		Limit:        limit + 1,
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if int64(len(entries)) > limit {
		entries = entries[:limit]
		next = entries[len(entries)-1].ID.Base36()
	}
	if entries == nil {
		entries = []*domain.AuditEntry{}
	}
	return entries, next, nil
}

type auditTransactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// auditedWrite runs write and records the entry it describes atomically. A
// nil recorder runs write alone; entry returning nil skips the record.
func auditedWrite(ctx context.Context, rec AuditRecorder, write func(ctx context.Context) error, entry func() *domain.AuditEntry) error {
	if rec == nil {
		return write(ctx)
	}
	run := func(ctx context.Context) error {
		if err := write(ctx); err != nil {
			return err
		}
		e := entry()
		if e == nil {
			return nil
		}
		return rec.Record(ctx, *e)
	}
	if tx, ok := rec.(auditTransactor); ok {
		return tx.WithinTx(ctx, run)
	}
	return run(ctx)
}

// AuditedWrite exposes auditedWrite to recorders outside this package.
func AuditedWrite(ctx context.Context, rec AuditRecorder, write func(ctx context.Context) error, entry func() *domain.AuditEntry) error {
	return auditedWrite(ctx, rec, write, entry)
}

func projectAudit(action string, projectID snow.ID, targetType, targetID string, metadata map[string]any) *domain.AuditEntry {
	return &domain.AuditEntry{
		Action:     action,
		ProjectID:  domain.IDPtr(projectID),
		TargetType: targetType,
		TargetID:   targetID,
		Metadata:   metadata,
	}
}

func orgAudit(action string, orgID snow.ID, targetType, targetID string, metadata map[string]any) *domain.AuditEntry {
	return &domain.AuditEntry{
		Action:     action,
		OrgID:      domain.IDPtr(orgID),
		TargetType: targetType,
		TargetID:   targetID,
		Metadata:   metadata,
	}
}
