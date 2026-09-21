package usecase

import (
	"context"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const permissionCacheTTL = 30 * time.Second

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=permission_mock_test.go -package=usecase
type pbacRepository interface {
	ListEffectiveRules(ctx context.Context, projectID snow.ID, userID snow.ID) ([]*domain.PBACRule, error)
	ListRulesByProject(ctx context.Context, projectID snow.ID) ([]*domain.PBACRule, error)
	CreateRule(ctx context.Context, rule domain.PBACRule) (*domain.PBACRule, error)
	GetRuleForProject(ctx context.Context, projectID snow.ID, ruleID int64) (*domain.PBACRule, error)
	DeleteRuleForProject(ctx context.Context, projectID snow.ID, ruleID int64) error
	ListPathPermissions(ctx context.Context, projectID snow.ID) ([]*domain.ProjectPathPermission, error)
	UpsertPathPermission(ctx context.Context, perm domain.ProjectPathPermission) (*domain.ProjectPathPermission, error)
	DeletePathPermission(ctx context.Context, projectID snow.ID, pathPrefix string) error
}

type userLookup interface {
	GetByID(ctx context.Context, id snow.ID) (*domain.User, error)
}

type groupLookup interface {
	GetByID(ctx context.Context, id snow.ID) (*domain.Group, error)
}

type permissionCacheKey struct {
	projectID snow.ID
	userID    snow.ID
}

type permissionCacheEntry struct {
	expires time.Time
	version uint64
	set     *permissionSet
}

type permissionSet struct {
	rules    []*domain.PBACRule
	defaults []*domain.ProjectPathPermission
}

type Permission struct {
	repo   pbacRepository
	users  userLookup
	groups groupLookup
	orgs   orgAuthorizer

	mu       sync.Mutex
	cache    map[permissionCacheKey]permissionCacheEntry
	versions map[snow.ID]uint64
}

func NewPermission(repo pbacRepository, users userLookup, groups groupLookup, orgs orgAuthorizer) *Permission {
	return &Permission{
		repo:     repo,
		users:    users,
		groups:   groups,
		orgs:     orgs,
		cache:    map[permissionCacheKey]permissionCacheEntry{},
		versions: map[snow.ID]uint64{},
	}
}

// HasProjectAccess reports whether the caller holds permission on any path of
// the project. It gates project-wide operations such as listing branches.
func (p *Permission) HasProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) bool {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return false
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return true
	}
	set, err := p.load(ctx, projectID, claim.UserID)
	if err != nil {
		return false
	}
	return set.grantsAnywhere(permission)
}

// Effective resolves the permission mask for a single path.
func (p *Permission) Effective(ctx context.Context, projectID snow.ID, path string) domain.Permission {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return 0
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return domain.PermissionAll
	}
	set, err := p.load(ctx, projectID, claim.UserID)
	if err != nil {
		return 0
	}
	return set.effective(path)
}

// HasPathAccess reports whether the caller holds every bit of permission on path.
func (p *Permission) HasPathAccess(ctx context.Context, projectID snow.ID, path string, permission domain.Permission) bool {
	return p.Effective(ctx, projectID, path).Has(permission)
}

func (p *Permission) requireAdmin(ctx context.Context, projectID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return nil
	}
	if p.Effective(ctx, projectID, "").Has(domain.PermissionAdmin) {
		return nil
	}
	return domain.NewErrorNoPermission()
}

func (p *Permission) requireOrgAdmin(ctx context.Context, orgID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return nil
	}
	owner, err := p.orgs.IsOrgOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if !owner {
		return domain.NewErrorNoPermission()
	}
	return nil
}

// CompileFilter resolves the caller's rules once so a manifest walk can prune
// directories without a repository round-trip per path.
func (p *Permission) CompileFilter(ctx context.Context, projectID snow.ID, permission domain.Permission) (*PathFilter, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return AllowAllFilter(), nil
	}
	set, err := p.load(ctx, projectID, claim.UserID)
	if err != nil {
		return nil, err
	}
	return &PathFilter{set: set, permission: permission}, nil
}

// PathFilter answers path visibility questions from a compiled rule set.
type PathFilter struct {
	all        bool
	set        *permissionSet
	permission domain.Permission
}

// AllowAllFilter returns a filter that permits every path.
func AllowAllFilter() *PathFilter {
	return &PathFilter{all: true}
}

// All reports whether the filter permits every path.
func (f *PathFilter) All() bool {
	return f != nil && f.all
}

// Allow reports whether path carries every bit of the filter's permission.
func (f *PathFilter) Allow(path string) bool {
	if f == nil {
		return false
	}
	if f.all {
		return true
	}
	return f.set.effective(path).Has(f.permission)
}

// CanDescend reports whether any path below dir could be allowed.
func (f *PathFilter) CanDescend(dir string) bool {
	if f == nil {
		return false
	}
	if f.all {
		return true
	}
	for _, rule := range f.set.rules {
		if domain.PrefixCanDescend(rule.PathPrefix, dir) {
			return true
		}
	}
	for _, def := range f.set.defaults {
		if domain.PrefixCanDescend(def.PathPrefix, dir) {
			return true
		}
	}
	return false
}

func (p *Permission) CreateRule(ctx context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
	if rule.ProjectID != nil {
		if err := p.requireAdmin(ctx, *rule.ProjectID); err != nil {
			return nil, err
		}
	} else if err := p.requireOrgAdmin(ctx, rule.OrgID); err != nil {
		return nil, err
	}
	if (rule.UserID == nil) == (rule.GroupID == nil) {
		return nil, domain.NewErrorUser("rule must target exactly one user or group")
	}
	if rule.Permission == 0 {
		return nil, domain.NewErrorUser("rule permission must not be empty")
	}
	if !rule.Permission.IsValid() {
		return nil, domain.NewErrorUser("invalid rule permission")
	}
	prefix, err := domain.NormalizePathPrefix(rule.PathPrefix)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	rule.PathPrefix = prefix

	if rule.UserID != nil {
		if _, err := p.users.GetByID(ctx, *rule.UserID); err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorUser("user not found")
			}
			return nil, err
		}
	}
	if rule.GroupID != nil {
		group, err := p.groups.GetByID(ctx, *rule.GroupID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorUser("group not found")
			}
			return nil, err
		}
		if group.OrgID != rule.OrgID {
			return nil, domain.NewErrorUser("group not found")
		}
	}

	created, err := p.repo.CreateRule(ctx, rule)
	if err != nil {
		return nil, err
	}
	p.invalidateAll()
	return created, nil
}

func (p *Permission) DeleteRule(ctx context.Context, projectID snow.ID, ruleID int64) error {
	if err := p.requireAdmin(ctx, projectID); err != nil {
		return err
	}
	rule, err := p.repo.GetRuleForProject(ctx, projectID, ruleID)
	if err != nil {
		return err
	}
	if rule.ProjectID == nil {
		if err := p.requireOrgAdmin(ctx, rule.OrgID); err != nil {
			return err
		}
	}
	if err := p.repo.DeleteRuleForProject(ctx, projectID, ruleID); err != nil {
		return err
	}
	p.invalidateAll()
	return nil
}

func (p *Permission) ListRules(ctx context.Context, projectID snow.ID) ([]*domain.PBACRule, error) {
	if err := p.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	return p.repo.ListRulesByProject(ctx, projectID)
}

func (p *Permission) ListPathPermissions(ctx context.Context, projectID snow.ID) ([]*domain.ProjectPathPermission, error) {
	if err := p.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	return p.repo.ListPathPermissions(ctx, projectID)
}

// MyPermissions returns the caller's effective project mask plus the rules and
// defaults that produced it.
func (p *Permission) MyPermissions(ctx context.Context, projectID snow.ID) (domain.Permission, []*domain.PBACRule, []*domain.ProjectPathPermission, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return 0, nil, nil, domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		defaults, err := p.repo.ListPathPermissions(ctx, projectID)
		if err != nil {
			return 0, nil, nil, err
		}
		return domain.PermissionAll, nil, defaults, nil
	}
	set, err := p.load(ctx, projectID, claim.UserID)
	if err != nil {
		return 0, nil, nil, err
	}
	var mask domain.Permission
	for _, rule := range set.rules {
		mask |= rule.Permission
	}
	for _, def := range set.defaults {
		mask |= def.Permission
	}
	return mask, set.rules, set.defaults, nil
}

func (p *Permission) AdminHasProject(ctx context.Context, projectID snow.ID) bool {
	return p.requireAdmin(ctx, projectID) == nil
}

// InvalidateAll drops cached rule sets after changes made outside this usecase.
func (p *Permission) InvalidateAll() {
	p.invalidateAll()
}

func (p *Permission) SetPathPermission(ctx context.Context, projectID snow.ID, pathPrefix string, permission domain.Permission) (*domain.ProjectPathPermission, error) {
	if err := p.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	if !permission.IsValid() {
		return nil, domain.NewErrorUser("invalid permission")
	}
	prefix, err := domain.NormalizePathPrefix(pathPrefix)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	perm, err := p.repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID:  projectID,
		PathPrefix: prefix,
		Permission: permission,
	})
	if err != nil {
		return nil, err
	}
	p.invalidateAll()
	return perm, nil
}

func (p *Permission) DeletePathPermission(ctx context.Context, projectID snow.ID, pathPrefix string) error {
	if err := p.requireAdmin(ctx, projectID); err != nil {
		return err
	}
	prefix, err := domain.NormalizePathPrefix(pathPrefix)
	if err != nil {
		return domain.NewErrorUser(err.Error())
	}
	if err := p.repo.DeletePathPermission(ctx, projectID, prefix); err != nil {
		return err
	}
	p.invalidateAll()
	return nil
}

func (p *Permission) load(ctx context.Context, projectID snow.ID, userID snow.ID) (*permissionSet, error) {
	key := permissionCacheKey{projectID: projectID, userID: userID}
	now := time.Now()

	p.mu.Lock()
	version, ok := p.versions[projectID]
	if !ok {
		p.versions[projectID] = 0
	}
	if entry, ok := p.cache[key]; ok && entry.version == version && now.Before(entry.expires) {
		set := entry.set
		p.mu.Unlock()
		return set, nil
	}
	p.mu.Unlock()

	rules, err := p.repo.ListEffectiveRules(ctx, projectID, userID)
	if err != nil {
		return nil, err
	}
	defaults, err := p.repo.ListPathPermissions(ctx, projectID)
	if err != nil {
		return nil, err
	}

	set := &permissionSet{rules: rules, defaults: defaults}

	p.mu.Lock()
	if p.versions[projectID] == version {
		p.cache[key] = permissionCacheEntry{
			expires: now.Add(permissionCacheTTL),
			version: version,
			set:     set,
		}
	}
	p.mu.Unlock()
	return set, nil
}

func (p *Permission) invalidateAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for projectID := range p.versions {
		p.versions[projectID]++
	}
	p.cache = map[permissionCacheKey]permissionCacheEntry{}
}

func (s *permissionSet) grantsAnywhere(permission domain.Permission) bool {
	if permission == 0 {
		return false
	}
	for _, rule := range s.rules {
		if rule.Permission&permission != 0 {
			return true
		}
	}
	for _, def := range s.defaults {
		if def.Permission&permission != 0 {
			return true
		}
	}
	return false
}

func (s *permissionSet) effective(path string) domain.Permission {
	var mask domain.Permission
	for _, rule := range s.rules {
		if domain.PrefixCovers(rule.PathPrefix, path) {
			mask |= rule.Permission
		}
	}

	var deepest *domain.ProjectPathPermission
	for _, def := range s.defaults {
		if !domain.PrefixCovers(def.PathPrefix, path) {
			continue
		}
		if deepest == nil || len(def.PathPrefix) > len(deepest.PathPrefix) {
			deepest = def
		}
	}
	if deepest != nil {
		mask |= deepest.Permission
	}
	return mask
}
