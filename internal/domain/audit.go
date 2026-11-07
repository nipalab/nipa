package domain

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// ActorType says who performed an audited action.
type ActorType string

const (
	AuditActorUser     ActorType = "user"
	AuditActorProvider ActorType = "provider"
	AuditActorSystem   ActorType = "system"
)

// AuditEntry is one immutable audit log record. Metadata holds the old and
// new values of the change and must never carry secrets.
type AuditEntry struct {
	ID          snow.ID
	OccurredAt  time.Time
	ActorType   ActorType
	ActorUserID *snow.ID
	ActorLabel  string
	Action      string
	OrgID       *snow.ID
	ProjectID   *snow.ID
	TargetType  string
	TargetID    string
	Metadata    map[string]any
	RequestID   string
	ClientIP    string
	UserAgent   string
}

// AuditFilter narrows an audit log listing. After is the exclusive keyset
// cursor (the last id of the previous page).
type AuditFilter struct {
	ActorUserID  *snow.ID
	OrgID        *snow.ID
	ProjectID    *snow.ID
	ActionPrefix string
	TargetID     string
	Since        *time.Time
	Until        *time.Time
	After        *snow.ID
	Limit        int64
}

// RequestMeta is the caller's network identity, attached by the transport.
type RequestMeta struct {
	ClientIP  string
	UserAgent string
}

type requestMetaKey struct{}

func ContextWithRequestMeta(ctx context.Context, meta RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaKey{}, meta)
}

func RequestMetaFromContext(ctx context.Context) (RequestMeta, bool) {
	meta, ok := ctx.Value(requestMetaKey{}).(RequestMeta)
	return meta, ok
}

// IDPtr returns a pointer to a copy of id.
func IDPtr(id snow.ID) *snow.ID {
	return &id
}
