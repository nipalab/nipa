package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=maintenance_mock_test.go -package=usecase

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	maxMaintenanceMessageRunes = 500
	maxMaintenanceLead         = 90 * 24 * time.Hour
)

type maintenanceRepository interface {
	List(ctx context.Context) ([]domain.MaintenanceWindow, error)
	GetServer(ctx context.Context) (*domain.MaintenanceWindow, error)
	GetOrg(ctx context.Context, orgID snow.ID) (*domain.MaintenanceWindow, error)
	GetProject(ctx context.Context, projectID snow.ID) (*domain.MaintenanceWindow, error)
	UpsertServer(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error)
	UpsertOrg(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error)
	UpsertProject(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error)
	Delete(ctx context.Context, id snow.ID) (bool, error)
	RecipientsServer(ctx context.Context) ([]domain.MaintenanceRecipient, error)
	RecipientsOrg(ctx context.Context, orgID snow.ID) ([]domain.MaintenanceRecipient, error)
	RecipientsProject(ctx context.Context, projectID snow.ID) ([]domain.MaintenanceRecipient, error)
}

type maintenanceResolver interface {
	ResolveOrg(ctx context.Context, orgSlug string) (*domain.Organization, error)
	ResolveBySlug(ctx context.Context, orgSlug, projectSlug string) (*domain.Organization, *domain.Project, error)
}

type maintenanceOrgs interface {
	IsOrgOwner(ctx context.Context, orgID snow.ID) (bool, error)
	IsOrgMember(ctx context.Context, orgID snow.ID) (bool, error)
}

type maintenanceAccess interface {
	HasProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) bool
}

type MaintenanceNotice string

const (
	MaintenanceNoticeScheduled MaintenanceNotice = "scheduled"
	MaintenanceNoticeUpdated   MaintenanceNotice = "updated"
	MaintenanceNoticeFinished  MaintenanceNotice = "finished"
)

// MaintenanceMailer announces a window to its recipients. It runs inside the
// window's transaction.
type MaintenanceMailer interface {
	Announce(ctx context.Context, notice MaintenanceNotice, window domain.MaintenanceWindow, recipients []domain.MaintenanceRecipient) error
}

// MaintenanceInput is a window to store. A missing StartsAt keeps the stored
// start of an existing window, else it means now.
type MaintenanceInput struct {
	Mode     string
	Message  string
	StartsAt *time.Time
	EndsAt   *time.Time
	Notify   bool
}

// MaintenanceState is a status answer; only the fields of the asked scope are
// filled.
type MaintenanceState struct {
	Server   *maintenance.Status
	Source   string
	Org      *maintenance.Status
	Project  *maintenance.Status
	Orgs     []maintenance.Status
	Projects []maintenance.Status
}

// Maintenance reads and changes maintenance windows. Every write is applied
// to the local Service after its transaction committed; other instances
// catch up on their next poll.
type Maintenance struct {
	repo     maintenanceRepository
	resolver maintenanceResolver
	orgs     maintenanceOrgs
	perm     maintenanceAccess
	svc      *maintenance.Service
	node     snow.Node
	audit    AuditRecorder
	mailer   MaintenanceMailer
	system   bool
}

func NewMaintenance(repo maintenanceRepository, resolver maintenanceResolver, orgs maintenanceOrgs, perm maintenanceAccess, svc *maintenance.Service, node snow.Node) *Maintenance {
	if svc == nil {
		svc = maintenance.NewService()
	}
	return &Maintenance{repo: repo, resolver: resolver, orgs: orgs, perm: perm, svc: svc, node: node}
}

func (m *Maintenance) WithAudit(rec AuditRecorder) *Maintenance {
	m.audit = rec
	return m
}

// WithMailer announces windows stored with notify. Nil sends nothing.
func (m *Maintenance) WithMailer(mailer MaintenanceMailer) *Maintenance {
	m.mailer = mailer
	return m
}

// WithSystemActor returns a copy for the server command line: no permission
// checks, audited as the system with via "cli", created_by 0.
func (m *Maintenance) WithSystemActor() *Maintenance {
	c := *m
	c.system = true
	return &c
}

func (m *Maintenance) Service() *maintenance.Service {
	return m.svc
}

// WindowsFromDomain converts stored windows for a snapshot.
func WindowsFromDomain(rows []domain.MaintenanceWindow) []maintenance.Window {
	out := make([]maintenance.Window, 0, len(rows))
	for _, row := range rows {
		out = append(out, maintenance.FromDomain(row))
	}
	return out
}

// Status is the public answer: the server window and where it comes from.
func (m *Maintenance) Status(_ context.Context) MaintenanceState {
	snap := m.svc.Snapshot()
	return MaintenanceState{Server: statusOf(snap.Server, m.svc.Now()), Source: snap.Source}
}

// AdminStatus adds every org and project window (super admin).
func (m *Maintenance) AdminStatus(ctx context.Context) (MaintenanceState, error) {
	if err := m.requireSuperAdmin(ctx); err != nil {
		return MaintenanceState{}, err
	}
	snap, now := m.svc.Snapshot(), m.svc.Now()
	state := MaintenanceState{Server: statusOf(snap.Server, now), Source: snap.Source}
	state.Orgs = sortedStatuses(snap.Orgs, now, nil)
	state.Projects = sortedStatuses(snap.Projects, now, nil)
	return state, nil
}

// OrgStatus answers the server and org windows to org members, plus the
// windows of the org's projects to its owners.
func (m *Maintenance) OrgStatus(ctx context.Context, orgSlug string) (MaintenanceState, error) {
	org, err := m.resolver.ResolveOrg(ctx, orgSlug)
	if err != nil {
		return MaintenanceState{}, err
	}
	owner := m.system
	if !m.system {
		member, err := m.orgs.IsOrgMember(ctx, org.ID)
		if err != nil {
			return MaintenanceState{}, err
		}
		if !member {
			return MaintenanceState{}, domain.NewErrorNoPermission()
		}
		if owner, err = m.orgs.IsOrgOwner(ctx, org.ID); err != nil {
			return MaintenanceState{}, err
		}
	}
	snap, now := m.svc.Snapshot(), m.svc.Now()
	state := MaintenanceState{Server: statusOf(snap.Server, now), Org: statusOf(snap.Orgs[org.Slug], now)}
	if owner {
		state.Projects = sortedStatuses(snap.Projects, now, func(w *maintenance.Window) bool { return w.Org == org.Slug })
	}
	return state, nil
}

// ProjectStatus answers the server, org and project windows to project readers.
func (m *Maintenance) ProjectStatus(ctx context.Context, orgSlug, projectSlug string) (MaintenanceState, error) {
	org, project, err := m.resolver.ResolveBySlug(ctx, orgSlug, projectSlug)
	if err != nil {
		return MaintenanceState{}, err
	}
	if !m.system && !m.perm.HasProjectAccess(ctx, project.ID, domain.PermissionRead) {
		return MaintenanceState{}, domain.NewErrorNoPermission()
	}
	snap, now := m.svc.Snapshot(), m.svc.Now()
	return MaintenanceState{
		Server:  statusOf(snap.Server, now),
		Org:     statusOf(snap.Orgs[org.Slug], now),
		Project: statusOf(snap.Projects[maintenance.ProjectKey(org.Slug, project.Slug)], now),
	}, nil
}

func (m *Maintenance) SetServer(ctx context.Context, in MaintenanceInput) (*maintenance.Status, error) {
	if err := m.requireServerWrite(ctx); err != nil {
		return nil, err
	}
	return m.set(ctx, maintenanceTarget{scope: maintenance.ScopeServer}, in)
}

func (m *Maintenance) SetOrg(ctx context.Context, orgSlug string, in MaintenanceInput) (*maintenance.Status, error) {
	org, err := m.resolver.ResolveOrg(ctx, orgSlug)
	if err != nil {
		return nil, err
	}
	if err := m.requireOrgOwner(ctx, org.ID); err != nil {
		return nil, err
	}
	return m.set(ctx, maintenanceTarget{scope: maintenance.ScopeOrg, org: org}, in)
}

func (m *Maintenance) SetProject(ctx context.Context, orgSlug, projectSlug string, in MaintenanceInput) (*maintenance.Status, error) {
	org, project, err := m.resolver.ResolveBySlug(ctx, orgSlug, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := m.requireOrgOwner(ctx, org.ID); err != nil {
		return nil, err
	}
	return m.set(ctx, maintenanceTarget{scope: maintenance.ScopeProject, org: org, project: project}, in)
}

func (m *Maintenance) EndServer(ctx context.Context) error {
	if err := m.requireServerWrite(ctx); err != nil {
		return err
	}
	return m.end(ctx, maintenanceTarget{scope: maintenance.ScopeServer})
}

func (m *Maintenance) EndOrg(ctx context.Context, orgSlug string) error {
	org, err := m.resolver.ResolveOrg(ctx, orgSlug)
	if err != nil {
		return err
	}
	if err := m.requireOrgOwner(ctx, org.ID); err != nil {
		return err
	}
	return m.end(ctx, maintenanceTarget{scope: maintenance.ScopeOrg, org: org})
}

func (m *Maintenance) EndProject(ctx context.Context, orgSlug, projectSlug string) error {
	org, project, err := m.resolver.ResolveBySlug(ctx, orgSlug, projectSlug)
	if err != nil {
		return err
	}
	if err := m.requireOrgOwner(ctx, org.ID); err != nil {
		return err
	}
	return m.end(ctx, maintenanceTarget{scope: maintenance.ScopeProject, org: org, project: project})
}

type maintenanceTarget struct {
	scope   maintenance.Scope
	org     *domain.Organization
	project *domain.Project
}

func (m *Maintenance) set(ctx context.Context, t maintenanceTarget, in MaintenanceInput) (*maintenance.Status, error) {
	mode, err := maintenance.ParseMode(strings.TrimSpace(in.Mode))
	if err != nil {
		return nil, domain.NewErrorUser("mode must be read_only, full or frozen")
	}
	if mode == maintenance.Frozen && t.scope != maintenance.ScopeServer {
		return nil, domain.NewErrorUser("frozen is only available for the server window")
	}
	if utf8.RuneCountInString(in.Message) > maxMaintenanceMessageRunes {
		return nil, domain.NewErrorUser(fmt.Sprintf("message must be at most %d characters", maxMaintenanceMessageRunes))
	}
	existing, err := m.get(ctx, t)
	if err != nil {
		return nil, err
	}
	now := m.svc.Now()
	startsAt := now
	switch {
	case in.StartsAt != nil:
		startsAt = *in.StartsAt
	case existing != nil:
		startsAt = existing.StartsAt
	}
	if startsAt.After(now.Add(maxMaintenanceLead)) {
		return nil, domain.NewErrorUser("starts_at must be within 90 days")
	}
	var endsAt *time.Time
	if in.EndsAt != nil {
		if !in.EndsAt.After(startsAt) {
			return nil, domain.NewErrorUser("ends_at must be after starts_at")
		}
		e := in.EndsAt.UTC()
		endsAt = &e
	}
	w := domain.MaintenanceWindow{
		ID:        m.node.Generate(),
		Mode:      string(mode),
		Message:   in.Message,
		StartsAt:  startsAt.UTC(),
		EndsAt:    endsAt,
		Notify:    in.Notify,
		CreatedBy: m.actorID(ctx),
		UpdatedAt: now.UTC(),
	}
	t.fill(&w)
	notice, action := MaintenanceNoticeScheduled, domain.AuditMaintenanceScheduled
	if existing != nil {
		notice, action = MaintenanceNoticeUpdated, domain.AuditMaintenanceUpdated
	}
	var saved *domain.MaintenanceWindow
	notified := false
	err = auditedWrite(ctx, m.audit, func(ctx context.Context) error {
		var err error
		if saved, err = m.upsert(ctx, t, w); err != nil {
			return err
		}
		t.fill(saved)
		notified, err = m.announce(ctx, t, notice, *saved)
		return err
	}, func() *domain.AuditEntry {
		return m.auditEntry(action, t, *saved)
	})
	if err != nil {
		return nil, err
	}
	m.apply(ctx)
	st := maintenance.FromDomain(*saved)
	status := st.Status(m.svc.Now())
	status.Notified = &notified
	return &status, nil
}

func (m *Maintenance) end(ctx context.Context, t maintenanceTarget) error {
	existing, err := m.get(ctx, t)
	if err != nil {
		return err
	}
	if existing == nil {
		return domain.NewErrorNotFound("no maintenance window")
	}
	t.fill(existing)
	err = auditedWrite(ctx, m.audit, func(ctx context.Context) error {
		deleted, err := m.repo.Delete(ctx, existing.ID)
		if err != nil {
			return err
		}
		if !deleted {
			return domain.NewErrorNotFound("no maintenance window")
		}
		_, err = m.announce(ctx, t, MaintenanceNoticeFinished, *existing)
		return err
	}, func() *domain.AuditEntry {
		return m.auditEntry(domain.AuditMaintenanceEnded, t, *existing)
	})
	if err != nil {
		return err
	}
	m.apply(ctx)
	return nil
}

func (t maintenanceTarget) fill(w *domain.MaintenanceWindow) {
	if t.org != nil {
		w.OrgSlug = t.org.Slug
	}
	switch t.scope {
	case maintenance.ScopeOrg:
		w.OrgID = domain.IDPtr(t.org.ID)
	case maintenance.ScopeProject:
		w.ProjectID = domain.IDPtr(t.project.ID)
		w.ProjectSlug = t.project.Slug
	}
}

func (m *Maintenance) get(ctx context.Context, t maintenanceTarget) (*domain.MaintenanceWindow, error) {
	var w *domain.MaintenanceWindow
	var err error
	switch t.scope {
	case maintenance.ScopeOrg:
		w, err = m.repo.GetOrg(ctx, t.org.ID)
	case maintenance.ScopeProject:
		w, err = m.repo.GetProject(ctx, t.project.ID)
	default:
		w, err = m.repo.GetServer(ctx)
	}
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	return w, err
}

func (m *Maintenance) upsert(ctx context.Context, t maintenanceTarget, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	switch t.scope {
	case maintenance.ScopeOrg:
		return m.repo.UpsertOrg(ctx, w)
	case maintenance.ScopeProject:
		return m.repo.UpsertProject(ctx, w)
	default:
		return m.repo.UpsertServer(ctx, w)
	}
}

func (m *Maintenance) announce(ctx context.Context, t maintenanceTarget, notice MaintenanceNotice, w domain.MaintenanceWindow) (bool, error) {
	if m.mailer == nil || !w.Notify {
		return false, nil
	}
	var recipients []domain.MaintenanceRecipient
	var err error
	switch t.scope {
	case maintenance.ScopeOrg:
		recipients, err = m.repo.RecipientsOrg(ctx, t.org.ID)
	case maintenance.ScopeProject:
		recipients, err = m.repo.RecipientsProject(ctx, t.project.ID)
	default:
		recipients, err = m.repo.RecipientsServer(ctx)
	}
	if err != nil {
		return false, err
	}
	if err := m.mailer.Announce(ctx, notice, w, recipients); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Maintenance) auditEntry(action string, t maintenanceTarget, w domain.MaintenanceWindow) *domain.AuditEntry {
	metadata := map[string]any{
		"scope":     string(t.scope),
		"mode":      w.Mode,
		"starts_at": w.StartsAt.UTC().Format(time.RFC3339),
		"notify":    w.Notify,
	}
	if w.EndsAt != nil {
		metadata["ends_at"] = w.EndsAt.UTC().Format(time.RFC3339)
	}
	entry := &domain.AuditEntry{
		Action:     action,
		TargetType: domain.AuditTargetMaintenance,
		TargetID:   w.ID.Base36(),
		Metadata:   metadata,
	}
	if t.org != nil {
		entry.OrgID = domain.IDPtr(t.org.ID)
		metadata["org_id"] = t.org.ID.Base36()
	}
	if t.project != nil {
		entry.ProjectID = domain.IDPtr(t.project.ID)
		metadata["project_id"] = t.project.ID.Base36()
	}
	if m.system {
		entry.ActorType = domain.AuditActorSystem
		metadata["via"] = "cli"
	}
	return entry
}

// apply rebuilds the snapshot from the stored windows and keeps a current
// file override, so the change is visible here without waiting for a poll.
func (m *Maintenance) apply(ctx context.Context) {
	rows, err := m.repo.List(ctx)
	if err != nil {
		slog.Warn("maintenance: reload after write failed", "error", err)
		return
	}
	cur := m.svc.Snapshot()
	snap := maintenance.NewSnapshot(WindowsFromDomain(rows), m.svc.Now())
	if cur.Source == maintenance.SourceFile {
		snap.Server, snap.Source, snap.FileError = cur.Server, cur.Source, cur.FileError
	}
	if err := m.svc.Apply(ctx, snap); err != nil {
		slog.Warn("maintenance: local apply failed", "error", err)
	}
}

func (m *Maintenance) actorID(ctx context.Context) snow.ID {
	if m.system {
		return 0
	}
	claim, _ := domain.ClaimFromContext(ctx)
	return claim.UserID
}

func (m *Maintenance) requireSuperAdmin(ctx context.Context) error {
	if m.system {
		return nil
	}
	if claim, ok := domain.ClaimFromContext(ctx); ok && claim.IsSuperAdmin {
		return nil
	}
	return domain.NewErrorNoPermission()
}

func (m *Maintenance) requireServerWrite(ctx context.Context) error {
	if err := m.requireSuperAdmin(ctx); err != nil {
		return err
	}
	if m.svc.Snapshot().Source == maintenance.SourceFile {
		return domain.NewErrorConflict("maintenance is controlled by MAINTENANCE_FILE")
	}
	return nil
}

func (m *Maintenance) requireOrgOwner(ctx context.Context, orgID snow.ID) error {
	if m.system {
		return nil
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin {
		return nil
	}
	owner, err := m.orgs.IsOrgOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if !owner {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func statusOf(w *maintenance.Window, now time.Time) *maintenance.Status {
	if w == nil {
		return nil
	}
	st := w.Status(now)
	return &st
}

func sortedStatuses(windows map[string]*maintenance.Window, now time.Time, keep func(*maintenance.Window) bool) []maintenance.Status {
	keys := make([]string, 0, len(windows))
	for key, w := range windows {
		if keep == nil || keep(w) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]maintenance.Status, 0, len(keys))
	for _, key := range keys {
		out = append(out, windows[key].Status(now))
	}
	return out
}
