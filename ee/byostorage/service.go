// Package byostorage lets billing owners register their own S3-compatible
// servers and put projects on them: the servers' CRUD and probe, the org
// default, the project assignment at creation and the per-project store
// resolver.
//
// Enterprise Edition: see ee/LICENSE.
package byostorage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/sealbox"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

const (
	// MaxServersPerOwner caps the servers one user can register.
	MaxServersPerOwner = 20
	sealLabel          = "nipa byo storage secret v1"
	maxNameRunes       = 100
	probeBudget        = 90 * time.Second
)

type repository interface {
	LockOwner(ctx context.Context, owner snow.ID) error
	CountServers(ctx context.Context, owner snow.ID) (int64, error)
	CreateServer(ctx context.Context, server domain.StorageServer) (*domain.StorageServer, error)
	UpdateServer(ctx context.Context, server domain.StorageServer) (*domain.StorageServer, error)
	Server(ctx context.Context, id snow.ID) (*domain.StorageServer, error)
	ListServers(ctx context.Context, owner snow.ID) ([]domain.StorageServer, error)
	ServerInUse(ctx context.Context, id snow.ID) (bool, error)
	DeleteServer(ctx context.Context, id snow.ID) error
	OrgDefault(ctx context.Context, orgID snow.ID) (*snow.ID, error)
	SetOrgDefault(ctx context.Context, orgID, serverID, setBy snow.ID) error
	ClearOrgDefault(ctx context.Context, orgID snow.ID) (bool, error)
	AssignProject(ctx context.Context, projectID, serverID snow.ID) error
	ProjectServer(ctx context.Context, projectID snow.ID) (*domain.StorageServer, error)
}

// owners resolves an organization's billing owner; *storageusage.Ledger
// satisfies it. LookupBillingOwner never writes.
type owners interface {
	BillingOwner(ctx context.Context, orgID snow.ID) (snow.ID, error)
	LookupBillingOwner(ctx context.Context, orgID snow.ID) (snow.ID, error)
}

// orgAuthorizer answers the caller's organization role; *usecase.Org
// satisfies it.
type orgAuthorizer interface {
	IsOrgOwner(ctx context.Context, orgID snow.ID) (bool, error)
	IsOrgMember(ctx context.Context, orgID snow.ID) (bool, error)
}

type transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Entitlement reports whether an owner may write to their own storage. A nil
// entitlement allows everyone.
type Entitlement interface {
	OwnStorageActive(ctx context.Context, ownerID snow.ID) (bool, error)
}

// Store is an opened customer server.
type Store interface {
	storage.ChunkStore
	Probe(ctx context.Context) error
}

// Opener opens a customer server without probing it.
type Opener func(ctx context.Context, cfg s3store.Config) (Store, error)

func openS3(ctx context.Context, cfg s3store.Config) (Store, error) {
	return s3store.New(ctx, cfg)
}

// Config tunes the service: Key is the BYO_STORAGE_ENCRYPTION_KEY material
// and AllowPrivate lifts the public-address checks (tests and development).
type Config struct {
	Key          []byte
	AllowPrivate bool
}

// Service manages storage servers and assigns projects to them.
type Service struct {
	repo         repository
	owners       owners
	orgs         orgAuthorizer
	tx           transactor
	node         snow.Node
	box          *sealbox.Box
	open         Opener
	transport    http.RoundTripper
	allowPrivate bool
	audit        usecase.AuditRecorder
	entitlement  Entitlement
}

func New(repo repository, owners owners, tx transactor, node snow.Node, cfg Config) (*Service, error) {
	box, err := sealbox.New(cfg.Key, sealLabel)
	if err != nil {
		return nil, err
	}
	return &Service{
		repo:         repo,
		owners:       owners,
		tx:           tx,
		node:         node,
		box:          box,
		open:         openS3,
		transport:    NewTransport(cfg.AllowPrivate),
		allowPrivate: cfg.AllowPrivate,
	}, nil
}

// WithOrgs attaches the organization role checks behind the org default.
func (s *Service) WithOrgs(orgs orgAuthorizer) *Service {
	s.orgs = orgs
	return s
}

// WithAudit records server changes, org defaults and project assignments.
func (s *Service) WithAudit(rec usecase.AuditRecorder) *Service {
	s.audit = rec
	return s
}

// WithEntitlement gates writes to own storage on the owner's add-on.
func (s *Service) WithEntitlement(e Entitlement) *Service {
	s.entitlement = e
	return s
}

// ServerInput is a new server.
type ServerInput struct {
	Name            string
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
}

// ServerPatch changes a server; nil fields keep their value. Bucket and
// Prefix are refused: they are fixed once saved.
type ServerPatch struct {
	Name            *string
	Endpoint        *string
	Region          *string
	AccessKeyID     *string
	SecretAccessKey *string
	Bucket          *string
	Prefix          *string
}

// ProbeFailure names the S3 capability a server failed.
type ProbeFailure struct {
	Check string
	Err   error
}

func (e *ProbeFailure) Error() string {
	return fmt.Sprintf("storage server check %s failed: %v", e.Check, e.Err)
}

func (e *ProbeFailure) Unwrap() error { return e.Err }

// ProbeOpen is the ProbeFailure check for a server that cannot be opened.
const ProbeOpen = "open"

// OrgDefault is an organization's default server as its members see it.
type OrgDefault struct {
	Server    *domain.StorageServer
	CanManage bool
}

func callerID(ctx context.Context) (snow.ID, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return 0, domain.NewErrorUnauthorized("authentication required")
	}
	return claim.UserID, nil
}

// List returns the caller's servers.
func (s *Service) List(ctx context.Context) ([]domain.StorageServer, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListServers(ctx, userID)
}

// Get returns one of the caller's servers; another user's server is a 404.
func (s *Service) Get(ctx context.Context, id snow.ID) (*domain.StorageServer, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	return s.owned(ctx, userID, id)
}

func (s *Service) owned(ctx context.Context, userID, id snow.ID) (*domain.StorageServer, error) {
	server, err := s.repo.Server(ctx, id)
	if domain.IsErrorNotFound(err) || (err == nil && server.OwnerUserID != userID) {
		return nil, domain.NewErrorNotFound("storage server not found")
	}
	return server, err
}

// Create probes a new server and stores it with its secret sealed.
func (s *Service) Create(ctx context.Context, in ServerInput) (*domain.StorageServer, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireEntitled(ctx, userID); err != nil {
		return nil, err
	}
	server, err := s.validate(domain.StorageServer{
		ID:          s.node.Generate(),
		OwnerUserID: userID,
		Name:        in.Name,
		Endpoint:    in.Endpoint,
		Region:      in.Region,
		Bucket:      in.Bucket,
		Prefix:      in.Prefix,
		AccessKeyID: in.AccessKeyID,
	})
	if err != nil {
		return nil, err
	}
	if err := validateBucket(server.Bucket); err != nil {
		return nil, err
	}
	if server.Prefix, err = normalizePrefix(server.Prefix); err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(in.SecretAccessKey)
	if secret == "" {
		return nil, domain.NewErrorUser("secret_access_key is required")
	}
	if err := s.probe(ctx, server, secret); err != nil {
		return nil, err
	}
	if server.SecretSealed, err = s.seal(server.ID, secret); err != nil {
		return nil, err
	}
	var created *domain.StorageServer
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		return usecase.AuditedWrite(ctx, s.audit, func(ctx context.Context) error {
			if err := s.repo.LockOwner(ctx, userID); err != nil {
				return err
			}
			count, err := s.repo.CountServers(ctx, userID)
			if err != nil {
				return err
			}
			if count >= MaxServersPerOwner {
				return domain.NewErrorUser(fmt.Sprintf("you can register at most %d storage servers", MaxServersPerOwner))
			}
			created, err = s.repo.CreateServer(ctx, server)
			return nameConflict(err)
		}, func() *domain.AuditEntry {
			return serverAudit(domain.AuditStorageServerCreated, created, nil)
		})
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// Update changes a server's name, endpoint, region or keys and probes it
// again when anything but the name changed.
func (s *Service) Update(ctx context.Context, id snow.ID, patch ServerPatch) (*domain.StorageServer, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if patch.Bucket != nil || patch.Prefix != nil {
		return nil, domain.NewErrorUser("bucket and prefix cannot be changed")
	}
	current, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireEntitled(ctx, userID); err != nil {
		return nil, err
	}
	next := *current
	setString(&next.Name, patch.Name)
	setString(&next.Endpoint, patch.Endpoint)
	setString(&next.Region, patch.Region)
	setString(&next.AccessKeyID, patch.AccessKeyID)
	if next, err = s.validate(next); err != nil {
		return nil, err
	}
	var secret string
	if patch.SecretAccessKey != nil {
		if secret = strings.TrimSpace(*patch.SecretAccessKey); secret == "" {
			return nil, domain.NewErrorUser("secret_access_key cannot be empty")
		}
	}
	changed := changedFields(current, &next, secret != "")
	if len(changed) == 0 {
		return current, nil
	}
	if connectionChanged(changed) {
		probeSecret := secret
		if probeSecret == "" {
			if probeSecret, err = s.openSecret(current); err != nil {
				return nil, err
			}
		}
		if err := s.probe(ctx, next, probeSecret); err != nil {
			return nil, err
		}
	}
	if secret != "" {
		if next.SecretSealed, err = s.seal(next.ID, secret); err != nil {
			return nil, err
		}
	}
	var updated *domain.StorageServer
	err = usecase.AuditedWrite(ctx, s.audit, func(ctx context.Context) error {
		updated, err = s.repo.UpdateServer(ctx, next)
		return nameConflict(err)
	}, func() *domain.AuditEntry {
		return serverAudit(domain.AuditStorageServerUpdated, updated, map[string]any{"changed": changed})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Delete removes a server no project and no live organization uses.
func (s *Service) Delete(ctx context.Context, id snow.ID) error {
	userID, err := callerID(ctx)
	if err != nil {
		return err
	}
	server, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		return usecase.AuditedWrite(ctx, s.audit, func(ctx context.Context) error {
			inUse, err := s.repo.ServerInUse(ctx, id)
			if err != nil {
				return err
			}
			if inUse {
				return domain.NewErrorConflict("storage server is in use by a project or an organization default")
			}
			return s.repo.DeleteServer(ctx, id)
		}, func() *domain.AuditEntry {
			return serverAudit(domain.AuditStorageServerDeleted, server, nil)
		})
	})
}

// Test probes a stored server again; a failed check is a *ProbeFailure.
func (s *Service) Test(ctx context.Context, id snow.ID) error {
	userID, err := callerID(ctx)
	if err != nil {
		return err
	}
	server, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	secret, err := s.openSecret(server)
	if err != nil {
		return err
	}
	return s.probe(ctx, *server, secret)
}

// OrgDefault returns an organization's default server to its members;
// CanManage is true for an org owner who is also the billing owner.
func (s *Service) OrgDefault(ctx context.Context, orgID snow.ID) (*OrgDefault, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireOrgRole(ctx, orgID, false); err != nil {
		return nil, err
	}
	result := &OrgDefault{}
	if result.CanManage, err = s.canManage(ctx, orgID, userID); err != nil {
		return nil, err
	}
	serverID, err := s.repo.OrgDefault(ctx, orgID)
	if err != nil || serverID == nil {
		return result, err
	}
	if result.Server, err = s.repo.Server(ctx, *serverID); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) canManage(ctx context.Context, orgID, userID snow.ID) (bool, error) {
	owner, err := s.isOrgOwner(ctx, orgID)
	if err != nil || !owner {
		return false, err
	}
	billing, err := s.owners.LookupBillingOwner(ctx, orgID)
	if err != nil {
		return false, err
	}
	return billing == userID, nil
}

// SetOrgDefault sets (serverID) or clears (nil) the server new projects of
// the organization go to. Only an org owner who is the org's billing owner
// may set one, and only to a server of their own.
func (s *Service) SetOrgDefault(ctx context.Context, orgID snow.ID, serverID *snow.ID) error {
	userID, err := callerID(ctx)
	if err != nil {
		return err
	}
	if err := s.requireOrgRole(ctx, orgID, true); err != nil {
		return err
	}
	if serverID == nil {
		var cleared bool
		return usecase.AuditedWrite(ctx, s.audit, func(ctx context.Context) error {
			cleared, err = s.repo.ClearOrgDefault(ctx, orgID)
			return err
		}, func() *domain.AuditEntry {
			if !cleared {
				return nil
			}
			return orgDefaultAudit(domain.AuditOrgStorageDefaultCleared, orgID, nil)
		})
	}
	server, err := s.owned(ctx, userID, *serverID)
	if err != nil {
		return err
	}
	if err := s.requireEntitled(ctx, userID); err != nil {
		return err
	}
	var previous *snow.ID
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		billing, err := s.owners.BillingOwner(ctx, orgID)
		if err != nil {
			return err
		}
		if billing != userID {
			return domain.NewErrorForbidden("only the organization's billing owner can set its storage server")
		}
		return usecase.AuditedWrite(ctx, s.audit, func(ctx context.Context) error {
			if previous, err = s.repo.OrgDefault(ctx, orgID); err != nil {
				return err
			}
			return s.repo.SetOrgDefault(ctx, orgID, server.ID, userID)
		}, func() *domain.AuditEntry {
			if previous != nil && *previous == server.ID {
				return nil
			}
			return orgDefaultAudit(domain.AuditOrgStorageDefaultSet, orgID, map[string]any{
				"storage_server_id":  server.ID.Base36(),
				"previous_server_id": optionalID(previous),
			})
		})
	})
}

// ProjectServer returns the server a project is assigned to; nil when it
// uses the server's own storage. Callers check project read access.
func (s *Service) ProjectServer(ctx context.Context, projectID snow.ID) (*domain.StorageServer, error) {
	return s.repo.ProjectServer(ctx, projectID)
}

// Assign implements usecase.ProjectStorageAssigner inside the project's
// creation transaction: an explicit server must be the caller's and the
// caller the org's billing owner; otherwise the org default applies while
// its server still belongs to the billing owner; otherwise the project keeps
// the server's own storage.
func (s *Service) Assign(ctx context.Context, orgID, projectID snow.ID, requested *snow.ID) error {
	if requested != nil {
		userID, err := callerID(ctx)
		if err != nil {
			return err
		}
		server, err := s.owned(ctx, userID, *requested)
		if err != nil {
			return err
		}
		billing, err := s.owners.BillingOwner(ctx, orgID)
		if err != nil {
			return err
		}
		if billing != userID {
			return domain.NewErrorForbidden("only the organization's billing owner can choose a storage server")
		}
		return s.assign(ctx, orgID, projectID, server, "requested")
	}
	serverID, err := s.repo.OrgDefault(ctx, orgID)
	if err != nil || serverID == nil {
		return err
	}
	server, err := s.repo.Server(ctx, *serverID)
	if err != nil {
		return err
	}
	billing, err := s.owners.BillingOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if billing != server.OwnerUserID {
		return nil
	}
	return s.assign(ctx, orgID, projectID, server, "org_default")
}

func (s *Service) assign(ctx context.Context, orgID, projectID snow.ID, server *domain.StorageServer, source string) error {
	if err := s.requireEntitled(ctx, server.OwnerUserID); err != nil {
		return err
	}
	if err := s.repo.AssignProject(ctx, projectID, server.ID); err != nil {
		return err
	}
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, domain.AuditEntry{
		Action:     domain.AuditProjectStorageServerSet,
		OrgID:      domain.IDPtr(orgID),
		ProjectID:  domain.IDPtr(projectID),
		TargetType: domain.AuditTargetProject,
		TargetID:   projectID.Base36(),
		Metadata: map[string]any{
			"storage_server_id": server.ID.Base36(),
			"source":            source,
		},
	})
}

func (s *Service) requireOrgRole(ctx context.Context, orgID snow.ID, owner bool) error {
	if s.orgs == nil {
		return domain.NewErrorNoPermission()
	}
	var ok bool
	var err error
	if owner {
		ok, err = s.orgs.IsOrgOwner(ctx, orgID)
	} else {
		ok, err = s.orgs.IsOrgMember(ctx, orgID)
	}
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func (s *Service) isOrgOwner(ctx context.Context, orgID snow.ID) (bool, error) {
	if s.orgs == nil {
		return false, nil
	}
	return s.orgs.IsOrgOwner(ctx, orgID)
}

func (s *Service) requireEntitled(ctx context.Context, ownerID snow.ID) error {
	active, err := entitled(ctx, s.entitlement, ownerID)
	if err != nil {
		return err
	}
	if !active {
		return usecase.ErrStorageReadOnly()
	}
	return nil
}

func entitled(ctx context.Context, e Entitlement, ownerID snow.ID) (bool, error) {
	if e == nil {
		return true, nil
	}
	return e.OwnStorageActive(ctx, ownerID)
}

func (s *Service) validate(server domain.StorageServer) (domain.StorageServer, error) {
	server.Name = strings.TrimSpace(server.Name)
	if server.Name == "" || utf8.RuneCountInString(server.Name) > maxNameRunes {
		return server, domain.NewErrorUser(fmt.Sprintf("name is required (at most %d characters)", maxNameRunes))
	}
	endpoint, err := NormalizeEndpoint(server.Endpoint, s.allowPrivate)
	if err != nil {
		return server, err
	}
	server.Endpoint = endpoint
	server.Region = strings.TrimSpace(server.Region)
	if len(server.Region) > 64 {
		return server, domain.NewErrorUser("region is too long")
	}
	server.Bucket = strings.TrimSpace(server.Bucket)
	server.AccessKeyID = strings.TrimSpace(server.AccessKeyID)
	if server.AccessKeyID == "" {
		return server, domain.NewErrorUser("access_key_id is required")
	}
	return server, nil
}

func (s *Service) storeConfig(server domain.StorageServer, secret string) s3store.Config {
	return s3store.Config{
		Endpoint:        server.Endpoint,
		Region:          server.Region,
		Bucket:          server.Bucket,
		Prefix:          server.Prefix,
		AccessKeyID:     server.AccessKeyID,
		SecretAccessKey: secret,
		Transport:       s.transport,
		SkipProbe:       true,
	}
}

func (s *Service) probe(ctx context.Context, server domain.StorageServer, secret string) error {
	ctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	store, err := s.open(ctx, s.storeConfig(server, secret))
	if err != nil {
		return &ProbeFailure{Check: ProbeOpen, Err: err}
	}
	defer func() { _ = store.Close() }()
	if err := store.Probe(ctx); err != nil {
		var probeErr *s3store.ProbeError
		if errors.As(err, &probeErr) {
			return &ProbeFailure{Check: probeErr.Check, Err: probeErr.Err}
		}
		return &ProbeFailure{Check: ProbeOpen, Err: err}
	}
	return nil
}

func sealAAD(id snow.ID) []byte {
	return []byte("storage_server:" + id.Base36())
}

func (s *Service) seal(id snow.ID, secret string) ([]byte, error) {
	sealed, err := s.box.Seal([]byte(secret), sealAAD(id))
	if err != nil {
		return nil, domain.NewErrorInternalServer("seal storage server secret: " + err.Error())
	}
	return sealed, nil
}

func (s *Service) openSecret(server *domain.StorageServer) (string, error) {
	plain, err := s.box.Open(server.SecretSealed, sealAAD(server.ID))
	if err != nil {
		return "", domain.NewErrorInternalServer("open storage server secret: " + err.Error())
	}
	return string(plain), nil
}

func setString(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

func changedFields(before, after *domain.StorageServer, secret bool) []string {
	var changed []string
	for _, field := range []struct {
		name          string
		before, after string
	}{
		{"name", before.Name, after.Name},
		{"endpoint", before.Endpoint, after.Endpoint},
		{"region", before.Region, after.Region},
		{"access_key_id", before.AccessKeyID, after.AccessKeyID},
	} {
		if field.before != field.after {
			changed = append(changed, field.name)
		}
	}
	if secret {
		changed = append(changed, "secret_access_key")
	}
	return changed
}

func connectionChanged(changed []string) bool {
	for _, field := range changed {
		if field != "name" {
			return true
		}
	}
	return false
}

func nameConflict(err error) error {
	if domain.IsErrorConflict(err) {
		return domain.NewErrorConflict("a storage server with this name already exists")
	}
	return err
}

func serverAudit(action string, server *domain.StorageServer, extra map[string]any) *domain.AuditEntry {
	metadata := map[string]any{
		"name":     server.Name,
		"endpoint": server.Endpoint,
		"bucket":   server.Bucket,
		"prefix":   server.Prefix,
	}
	for k, v := range extra {
		metadata[k] = v
	}
	return &domain.AuditEntry{
		Action:     action,
		TargetType: domain.AuditTargetStorageServer,
		TargetID:   server.ID.Base36(),
		Metadata:   metadata,
	}
}

func orgDefaultAudit(action string, orgID snow.ID, metadata map[string]any) *domain.AuditEntry {
	return &domain.AuditEntry{
		Action:     action,
		OrgID:      domain.IDPtr(orgID),
		TargetType: domain.AuditTargetOrg,
		TargetID:   orgID.Base36(),
		Metadata:   metadata,
	}
}

func optionalID(id *snow.ID) any {
	if id == nil {
		return nil
	}
	return id.Base36()
}
