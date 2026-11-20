package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=issue_tracker_mock_test.go -package=usecase

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/egress"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/issuetracker/jira"
	"github.com/nipalab/nipa/internal/sealbox"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	IssueTrackerSecretLabel = "nipa issue tracker secret v1"

	IssueTrackerCheckAuth  = "auth"
	IssueTrackerCheckReach = "reach"
	IssueTrackerCheckTLS   = "tls"

	maxIssueTrackerNameRunes = 100
	maxIssueTrackerURLBytes  = 2048
	maxIssueTrackerAccount   = 320
	maxIssueTrackerToken     = 8192
	maxProjectIssueKeys      = 20
	maxIssueStatusRunes      = 100
)

type issueTrackerRepository interface {
	LockOrg(ctx context.Context, orgID snow.ID) error
	GetByOrg(ctx context.Context, orgID snow.ID) (*domain.IssueTracker, error)
	Create(ctx context.Context, t domain.IssueTracker) (*domain.IssueTracker, error)
	Update(ctx context.Context, t domain.IssueTracker) (*domain.IssueTracker, error)
	Delete(ctx context.Context, orgID, id snow.ID) (bool, error)
	CountProjects(ctx context.Context, trackerID snow.ID) (int64, error)
	GetProject(ctx context.Context, projectID snow.ID) (*domain.ProjectIssueTracker, error)
	UpsertProject(ctx context.Context, s domain.ProjectIssueTracker) (*domain.ProjectIssueTracker, error)
	DeleteProject(ctx context.Context, projectID snow.ID) (bool, error)
}

type issueTrackerOrgs interface {
	IsOrgOwner(ctx context.Context, orgID snow.ID) (bool, error)
	IsOrgMember(ctx context.Context, orgID snow.ID) (bool, error)
}

type issueTrackerAccess interface {
	AdminHasProject(ctx context.Context, projectID snow.ID) bool
}

// IssueTrackerFactory builds a tracker client for a stored connection.
type IssueTrackerFactory func(kind string, cfg issuetracker.Config) (issuetracker.Tracker, error)

// IssueTrackerProbeFailure names the check a connection failed: auth, reach
// or tls.
type IssueTrackerProbeFailure struct {
	Check string
	Err   error
}

func (e *IssueTrackerProbeFailure) Error() string {
	return fmt.Sprintf("issue tracker check %s failed: %v", e.Check, e.Err)
}

func (e *IssueTrackerProbeFailure) Unwrap() error { return e.Err }

// IssueTrackerInput replaces the org's connection. A nil or empty Token keeps
// the stored one, unless the base URL changes.
type IssueTrackerInput struct {
	Kind     string
	Name     string
	BaseURL  string
	AuthKind string
	Account  string
	Token    *string
}

// IssueTrackerView is the connection as the caller may see it: org members
// get the kind, name and base URL only.
type IssueTrackerView struct {
	Tracker   *domain.IssueTracker
	CanManage bool
}

type IssueTrackerPutResult struct {
	Tracker *domain.IssueTracker
	Site    *issuetracker.Site
	Created bool
}

type IssueTrackerTestResult struct {
	OK    bool
	Check string
	Error string
	Site  *issuetracker.Site
}

// ProjectIssueTrackerInput replaces a project's settings; nil booleans
// default to true and an empty LinkCommits to off.
type ProjectIssueTrackerInput struct {
	TrackerID         *snow.ID
	Keys              []string
	LinkMergeRequests *bool
	LinkCommits       string
	CommentOnMerge    *bool
	MergedStatus      string
	OpenedStatus      string
	Active            *bool
}

// IssueTracker manages the org issue tracker connection and the project
// settings that use it. Exactly one connection per org is allowed; the
// service enforces it under a per-org lock.
type IssueTracker struct {
	repo    issueTrackerRepository
	orgs    issueTrackerOrgs
	perm    issueTrackerAccess
	box     *sealbox.Box
	factory IssueTrackerFactory
	node    snow.Node
	audit   AuditRecorder
	now     func() time.Time
}

// NewIssueTracker builds the usecase; a nil box makes every call answer 503.
func NewIssueTracker(repo issueTrackerRepository, orgs issueTrackerOrgs, perm issueTrackerAccess, box *sealbox.Box, node snow.Node) *IssueTracker {
	return &IssueTracker{
		repo:    repo,
		orgs:    orgs,
		perm:    perm,
		box:     box,
		factory: GuardedIssueTrackerFactory(nil),
		node:    node,
		now:     time.Now,
	}
}

// NewIssueTrackerBox derives the secret box from INTEGRATIONS_ENCRYPTION_KEY.
func NewIssueTrackerBox(key []byte) (*sealbox.Box, error) {
	return sealbox.New(key, IssueTrackerSecretLabel)
}

// GuardedIssueTrackerFactory builds trackers whose requests go through the
// egress guard with the given allowlist.
func GuardedIssueTrackerFactory(allowlist []string) IssueTrackerFactory {
	transport := egress.New(egress.Config{Allowlist: allowlist, Label: "issue tracker"}).Transport()
	return func(kind string, cfg issuetracker.Config) (issuetracker.Tracker, error) {
		cfg.Transport = transport
		return issuetracker.New(kind, cfg)
	}
}

func (u *IssueTracker) WithAudit(rec AuditRecorder) *IssueTracker {
	u.audit = rec
	return u
}

func (u *IssueTracker) WithFactory(f IssueTrackerFactory) *IssueTracker {
	u.factory = f
	return u
}

func (u *IssueTracker) WithClock(now func() time.Time) *IssueTracker {
	u.now = now
	return u
}

// Available reports whether INTEGRATIONS_ENCRYPTION_KEY is configured.
func (u *IssueTracker) Available() bool {
	return u.box != nil
}

func (u *IssueTracker) requireAvailable() error {
	if u.box == nil {
		return domain.NewErrorServiceUnavailable("the issue tracker integration is not configured on this server")
	}
	return nil
}

func (u *IssueTracker) requireOwner(ctx context.Context, orgID snow.ID) error {
	if err := u.requireAvailable(); err != nil {
		return err
	}
	owner, err := u.orgs.IsOrgOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if !owner {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func (u *IssueTracker) requireProjectAdmin(ctx context.Context, projectID snow.ID) error {
	if err := u.requireAvailable(); err != nil {
		return err
	}
	if !u.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return nil
}

// Get returns the org's connection; owners see every field but the token.
func (u *IssueTracker) Get(ctx context.Context, orgID snow.ID) (*IssueTrackerView, error) {
	if err := u.requireAvailable(); err != nil {
		return nil, err
	}
	owner, err := u.orgs.IsOrgOwner(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if !owner {
		member, err := u.orgs.IsOrgMember(ctx, orgID)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, domain.NewErrorNoPermission()
		}
	}
	tracker, err := u.lookup(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if tracker == nil {
		return nil, domain.NewErrorNotFound("no issue tracker connection")
	}
	if !owner {
		return &IssueTrackerView{Tracker: &domain.IssueTracker{Kind: tracker.Kind, Name: tracker.Name, BaseURL: tracker.BaseURL}}, nil
	}
	return &IssueTrackerView{Tracker: withoutSecret(tracker), CanManage: true}, nil
}

// Put creates or replaces the org's connection after a successful probe.
func (u *IssueTracker) Put(ctx context.Context, orgID snow.ID, in IssueTrackerInput) (*IssueTrackerPutResult, error) {
	if err := u.requireOwner(ctx, orgID); err != nil {
		return nil, err
	}
	claim, _ := domain.ClaimFromContext(ctx)
	want, err := normalizeIssueTrackerInput(in)
	if err != nil {
		return nil, err
	}
	existing, err := u.lookup(ctx, orgID)
	if err != nil {
		return nil, err
	}
	token := ""
	if in.Token != nil {
		token = strings.TrimSpace(*in.Token)
	}
	if len(token) > maxIssueTrackerToken {
		return nil, domain.NewErrorUser("token is too long")
	}
	tokenChanged := token != ""
	if !tokenChanged {
		switch {
		case existing == nil:
			return nil, domain.NewErrorUser("a token is required")
		case existing.BaseURL != want.BaseURL:
			return nil, domain.NewErrorUser("enter the token again when the base URL changes")
		}
		token, err = u.openSecret(existing)
		if err != nil {
			return nil, domain.NewErrorUser("the stored token cannot be decrypted; enter it again")
		}
	}
	site, err := u.probe(ctx, want, token)
	if err != nil {
		return nil, err
	}

	now := u.now().UTC()
	want.OrgID = orgID
	want.UpdatedAt = now
	if existing != nil {
		want.ID, want.CreatedBy, want.CreatedAt = existing.ID, existing.CreatedBy, existing.CreatedAt
	} else {
		want.ID, want.CreatedBy, want.CreatedAt = u.node.Generate(), claim.UserID, now
	}
	if want.SecretSealed, err = u.box.Seal([]byte(token), issueTrackerAAD(want.ID)); err != nil {
		return nil, domain.NewErrorInternalServer("seal issue tracker token: " + err.Error())
	}

	var saved *domain.IssueTracker
	err = auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		if err := u.repo.LockOrg(ctx, orgID); err != nil {
			return err
		}
		current, err := u.lookup(ctx, orgID)
		if err != nil {
			return err
		}
		if !sameIssueTracker(existing, current) {
			return domain.NewErrorConflict("the issue tracker connection changed meanwhile; try again")
		}
		if existing == nil {
			saved, err = u.repo.Create(ctx, want)
		} else {
			saved, err = u.repo.Update(ctx, want)
		}
		return err
	}, func() *domain.AuditEntry {
		action := domain.AuditIssueTrackerCreated
		metadata := issueTrackerAuditValue(saved)
		if existing != nil {
			action = domain.AuditIssueTrackerUpdated
			metadata["changed"] = issueTrackerChanges(existing, saved, tokenChanged)
		}
		return orgAudit(action, orgID, domain.AuditTargetIssueTracker, saved.ID.Base36(), metadata)
	})
	if err != nil {
		return nil, err
	}
	return &IssueTrackerPutResult{Tracker: withoutSecret(saved), Site: site, Created: existing == nil}, nil
}

// Delete removes the org's connection; it is a 409 while a project uses it.
func (u *IssueTracker) Delete(ctx context.Context, orgID snow.ID) error {
	if err := u.requireOwner(ctx, orgID); err != nil {
		return err
	}
	var removed *domain.IssueTracker
	return auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		if err := u.repo.LockOrg(ctx, orgID); err != nil {
			return err
		}
		current, err := u.lookup(ctx, orgID)
		if err != nil {
			return err
		}
		if current == nil {
			return domain.NewErrorNotFound("no issue tracker connection")
		}
		used, err := u.repo.CountProjects(ctx, current.ID)
		if err != nil {
			return err
		}
		if used > 0 {
			return domain.NewErrorConflict(fmt.Sprintf("the issue tracker connection is used by %d project(s); remove it from them first", used))
		}
		if _, err := u.repo.Delete(ctx, orgID, current.ID); err != nil {
			return err
		}
		removed = current
		return nil
	}, func() *domain.AuditEntry {
		return orgAudit(domain.AuditIssueTrackerDeleted, orgID, domain.AuditTargetIssueTracker, removed.ID.Base36(), issueTrackerAuditValue(removed))
	})
}

// Test probes the stored connection again. A failed check is a result, not
// an error.
func (u *IssueTracker) Test(ctx context.Context, orgID snow.ID) (*IssueTrackerTestResult, error) {
	if err := u.requireOwner(ctx, orgID); err != nil {
		return nil, err
	}
	tracker, err := u.lookup(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if tracker == nil {
		return nil, domain.NewErrorNotFound("no issue tracker connection")
	}
	token, err := u.openSecret(tracker)
	if err != nil {
		return &IssueTrackerTestResult{Check: IssueTrackerCheckAuth, Error: "the stored token cannot be decrypted; enter it again"}, nil
	}
	site, err := u.probe(ctx, *tracker, token)
	var failure *IssueTrackerProbeFailure
	switch {
	case err == nil:
		return &IssueTrackerTestResult{OK: true, Site: site}, nil
	case errors.As(err, &failure):
		return &IssueTrackerTestResult{Check: failure.Check, Error: failure.Error()}, nil
	}
	return nil, err
}

// GetProject returns a project's settings (project admin).
func (u *IssueTracker) GetProject(ctx context.Context, projectID snow.ID) (*domain.ProjectIssueTracker, error) {
	if err := u.requireProjectAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	settings, err := u.repo.GetProject(ctx, projectID)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound("the project does not use an issue tracker")
	}
	return settings, err
}

// PutProject points a project at its org's connection.
func (u *IssueTracker) PutProject(ctx context.Context, orgID, projectID snow.ID, in ProjectIssueTrackerInput) (*domain.ProjectIssueTracker, error) {
	if err := u.requireProjectAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	claim, _ := domain.ClaimFromContext(ctx)
	want, err := normalizeProjectIssueTracker(in)
	if err != nil {
		return nil, err
	}
	want.ProjectID = projectID
	want.SetBy = claim.UserID
	want.UpdatedAt = u.now().UTC()
	var saved *domain.ProjectIssueTracker
	err = auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		if err := u.repo.LockOrg(ctx, orgID); err != nil {
			return err
		}
		tracker, err := u.lookup(ctx, orgID)
		if err != nil {
			return err
		}
		if tracker == nil {
			return domain.NewErrorUser("the organization has no issue tracker connection")
		}
		if in.TrackerID != nil && *in.TrackerID != tracker.ID {
			return domain.NewErrorUser("tracker_id must be the organization's issue tracker connection")
		}
		want.TrackerID = tracker.ID
		saved, err = u.repo.UpsertProject(ctx, want)
		return err
	}, func() *domain.AuditEntry {
		return projectAudit(domain.AuditProjectIssueTrackerSet, projectID, domain.AuditTargetIssueTracker, saved.TrackerID.Base36(), projectIssueTrackerAuditValue(saved))
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// DeleteProject stops a project from using the org's connection.
func (u *IssueTracker) DeleteProject(ctx context.Context, projectID snow.ID) error {
	if err := u.requireProjectAdmin(ctx, projectID); err != nil {
		return err
	}
	var removed *domain.ProjectIssueTracker
	return auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		current, err := u.repo.GetProject(ctx, projectID)
		if domain.IsErrorNotFound(err) {
			return domain.NewErrorNotFound("the project does not use an issue tracker")
		}
		if err != nil {
			return err
		}
		if _, err := u.repo.DeleteProject(ctx, projectID); err != nil {
			return err
		}
		removed = current
		return nil
	}, func() *domain.AuditEntry {
		return projectAudit(domain.AuditProjectIssueTrackerCleared, projectID, domain.AuditTargetIssueTracker, removed.TrackerID.Base36(), projectIssueTrackerAuditValue(removed))
	})
}

func (u *IssueTracker) lookup(ctx context.Context, orgID snow.ID) (*domain.IssueTracker, error) {
	tracker, err := u.repo.GetByOrg(ctx, orgID)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	return tracker, err
}

func (u *IssueTracker) openSecret(t *domain.IssueTracker) (string, error) {
	plain, err := u.box.Open(t.SecretSealed, issueTrackerAAD(t.ID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (u *IssueTracker) probe(ctx context.Context, t domain.IssueTracker, token string) (*issuetracker.Site, error) {
	tracker, err := u.factory(t.Kind, issuetracker.Config{BaseURL: t.BaseURL, Account: t.Account, Secret: token, AuthKind: t.AuthKind})
	if err != nil {
		return nil, domain.NewErrorUser(trimKindPrefix(t.Kind, err))
	}
	site, err := tracker.Probe(ctx)
	if err != nil {
		return nil, &IssueTrackerProbeFailure{Check: probeCheck(err), Err: err}
	}
	return site, nil
}

func probeCheck(err error) string {
	if errors.Is(err, issuetracker.ErrUnauthorized) {
		return IssueTrackerCheckAuth
	}
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	if errors.As(err, &verify) || errors.As(err, &unknown) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &record) {
		return IssueTrackerCheckTLS
	}
	return IssueTrackerCheckReach
}

func normalizeIssueTrackerInput(in IssueTrackerInput) (domain.IssueTracker, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = jira.Kind
	}
	if !slices.Contains(issuetracker.Kinds(), kind) {
		return domain.IssueTracker{}, domain.NewErrorUser(fmt.Sprintf("unknown issue tracker kind %q", kind))
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.ToUpper(kind[:1]) + kind[1:]
	}
	if utf8.RuneCountInString(name) > maxIssueTrackerNameRunes {
		return domain.IssueTracker{}, domain.NewErrorUser(fmt.Sprintf("name must be at most %d characters", maxIssueTrackerNameRunes))
	}
	if len(in.BaseURL) > maxIssueTrackerURLBytes {
		return domain.IssueTracker{}, domain.NewErrorUser("base_url is too long")
	}
	baseURL, err := normalizeTrackerURL(kind, in.BaseURL)
	if err != nil {
		return domain.IssueTracker{}, domain.NewErrorUser(trimKindPrefix(kind, err))
	}
	account := strings.TrimSpace(in.Account)
	authKind := strings.ToLower(strings.TrimSpace(in.AuthKind))
	if authKind == "" {
		authKind = issuetracker.AuthBearer
		if account != "" {
			authKind = issuetracker.AuthBasic
		}
	}
	switch authKind {
	case issuetracker.AuthBasic:
		if account == "" {
			return domain.IssueTracker{}, domain.NewErrorUser("basic auth needs the account email")
		}
		if len(account) > maxIssueTrackerAccount {
			return domain.IssueTracker{}, domain.NewErrorUser("account is too long")
		}
	case issuetracker.AuthBearer:
		account = ""
	default:
		return domain.IssueTracker{}, domain.NewErrorUser(fmt.Sprintf("auth_kind must be %q or %q", issuetracker.AuthBasic, issuetracker.AuthBearer))
	}
	return domain.IssueTracker{Kind: kind, Name: name, BaseURL: baseURL, AuthKind: authKind, Account: account}, nil
}

func normalizeTrackerURL(kind, raw string) (string, error) {
	if kind == jira.Kind {
		return jira.NormalizeBaseURL(raw)
	}
	return strings.TrimRight(strings.TrimSpace(raw), "/"), nil
}

func normalizeProjectIssueTracker(in ProjectIssueTrackerInput) (domain.ProjectIssueTracker, error) {
	if len(in.Keys) == 0 {
		return domain.ProjectIssueTracker{}, domain.NewErrorUser("at least one project key is required")
	}
	keys, err := issuetracker.NormalizeProjectKeys(in.Keys)
	if err != nil {
		return domain.ProjectIssueTracker{}, domain.NewErrorUser(err.Error())
	}
	if len(keys) > maxProjectIssueKeys {
		return domain.ProjectIssueTracker{}, domain.NewErrorUser(fmt.Sprintf("at most %d project keys are allowed", maxProjectIssueKeys))
	}
	linkCommits := strings.TrimSpace(in.LinkCommits)
	if linkCommits == "" {
		linkCommits = domain.LinkCommitsOff
	}
	if !domain.IsValidLinkCommits(linkCommits) {
		return domain.ProjectIssueTracker{}, domain.NewErrorUser("link_commits must be off, default_branch or all")
	}
	merged, opened := strings.TrimSpace(in.MergedStatus), strings.TrimSpace(in.OpenedStatus)
	if utf8.RuneCountInString(merged) > maxIssueStatusRunes || utf8.RuneCountInString(opened) > maxIssueStatusRunes {
		return domain.ProjectIssueTracker{}, domain.NewErrorUser(fmt.Sprintf("statuses must be at most %d characters", maxIssueStatusRunes))
	}
	return domain.ProjectIssueTracker{
		Keys:              keys,
		LinkMergeRequests: boolOr(in.LinkMergeRequests, true),
		LinkCommits:       linkCommits,
		CommentOnMerge:    boolOr(in.CommentOnMerge, true),
		MergedStatus:      merged,
		OpenedStatus:      opened,
		IsActive:          boolOr(in.Active, true),
	}, nil
}

func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func trimKindPrefix(kind string, err error) string {
	return strings.TrimPrefix(err.Error(), kind+": ")
}

func issueTrackerAAD(id snow.ID) []byte {
	return []byte("issue_tracker:" + id.Base36())
}

func sameIssueTracker(a, b *domain.IssueTracker) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID && a.UpdatedAt.Equal(b.UpdatedAt)
}

func withoutSecret(t *domain.IssueTracker) *domain.IssueTracker {
	c := *t
	c.SecretSealed = nil
	return &c
}

func issueTrackerAuditValue(t *domain.IssueTracker) map[string]any {
	return map[string]any{
		"kind":      t.Kind,
		"name":      t.Name,
		"base_url":  t.BaseURL,
		"auth_kind": t.AuthKind,
	}
}

func issueTrackerChanges(old, cur *domain.IssueTracker, tokenChanged bool) []string {
	changed := []string{}
	for _, f := range []struct {
		name     string
		old, cur string
	}{
		{"kind", old.Kind, cur.Kind},
		{"name", old.Name, cur.Name},
		{"base_url", old.BaseURL, cur.BaseURL},
		{"auth_kind", old.AuthKind, cur.AuthKind},
		{"account", old.Account, cur.Account},
	} {
		if f.old != f.cur {
			changed = append(changed, f.name)
		}
	}
	if tokenChanged {
		changed = append(changed, "token")
	}
	return changed
}

func projectIssueTrackerAuditValue(s *domain.ProjectIssueTracker) map[string]any {
	return map[string]any{
		"tracker_id":          s.TrackerID.Base36(),
		"keys":                s.Keys,
		"link_merge_requests": s.LinkMergeRequests,
		"link_commits":        s.LinkCommits,
		"comment_on_merge":    s.CommentOnMerge,
		"merged_status":       s.MergedStatus,
		"opened_status":       s.OpenedStatus,
		"active":              s.IsActive,
	}
}
