package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=service_token_mock_test.go -package=usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	serviceTokenBytes        = 32
	serviceTokenHintLen      = len(domain.ServiceTokenPrefix) + 4
	maxServiceTokenNameRunes = 100
	maxServiceTokensActive   = 50
	serviceTokenTouchEvery   = time.Minute
)

type serviceTokenRepository interface {
	Create(ctx context.Context, token domain.ServiceToken) (*domain.ServiceToken, error)
	Get(ctx context.Context, projectID, id snow.ID) (*domain.ServiceToken, error)
	GetByHash(ctx context.Context, hash string) (*domain.ServiceToken, error)
	ListByProject(ctx context.Context, projectID snow.ID) ([]*domain.ServiceToken, error)
	LockProject(ctx context.Context, projectID snow.ID) error
	Revoke(ctx context.Context, projectID, id snow.ID, at time.Time) (*domain.ServiceToken, error)
	TouchLastUsed(ctx context.Context, id snow.ID, at time.Time) error
}

type serviceTokenProjectResolver interface {
	ResolveBySlug(ctx context.Context, orgSlug, projectSlug string) (*domain.Organization, *domain.Project, error)
}

// ServiceTokenInput describes a new service token.
type ServiceTokenInput struct {
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

// ServiceToken manages project service tokens and authenticates requests that
// carry one. A token acts as the project admin who created it, limited to its
// project and scopes.
type ServiceToken struct {
	repo     serviceTokenRepository
	perm     permissionUsecase
	users    userLookup
	projects serviceTokenProjectResolver
	snowNode snow.Node
	audit    AuditRecorder
	now      func() time.Time
}

func NewServiceToken(repo serviceTokenRepository, perm permissionUsecase, users userLookup, projects serviceTokenProjectResolver, snowNode snow.Node) *ServiceToken {
	return &ServiceToken{
		repo:     repo,
		perm:     perm,
		users:    users,
		projects: projects,
		snowNode: snowNode,
		now:      time.Now,
	}
}

// WithAudit records token creation and revocation. The token is never recorded.
func (s *ServiceToken) WithAudit(rec AuditRecorder) *ServiceToken {
	s.audit = rec
	return s
}

// Create mints a token and returns it with the plaintext, which is shown once.
func (s *ServiceToken) Create(ctx context.Context, projectID snow.ID, input ServiceTokenInput) (*domain.ServiceToken, string, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !s.perm.AdminHasProject(ctx, projectID) {
		return nil, "", domain.NewErrorNoPermission()
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, "", domain.NewErrorUser("a token name is required")
	}
	if utf8.RuneCountInString(name) > maxServiceTokenNameRunes {
		return nil, "", domain.NewErrorUser(fmt.Sprintf("token name must be at most %d characters", maxServiceTokenNameRunes))
	}
	scopes, err := normalizeServiceTokenScopes(input.Scopes)
	if err != nil {
		return nil, "", err
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(s.now()) {
		return nil, "", domain.NewErrorUser("expires_at must be in the future")
	}
	plaintext, err := generateServiceToken()
	if err != nil {
		return nil, "", domain.NewErrorInternalServer("generate service token: " + err.Error())
	}
	var created *domain.ServiceToken
	err = auditedWrite(ctx, s.audit, func(ctx context.Context) error {
		if err := s.repo.LockProject(ctx, projectID); err != nil {
			return err
		}
		existing, err := s.repo.ListByProject(ctx, projectID)
		if err != nil {
			return err
		}
		active := 0
		for _, token := range existing {
			if token.Active(s.now()) {
				active++
			}
		}
		if active >= maxServiceTokensActive {
			return domain.NewErrorUser(fmt.Sprintf("at most %d active service tokens are allowed per project", maxServiceTokensActive))
		}
		created, err = s.repo.Create(ctx, domain.ServiceToken{
			ID:        s.snowNode.Generate(),
			ProjectID: projectID,
			Name:      name,
			TokenHash: hashServiceToken(plaintext),
			Hint:      plaintext[:serviceTokenHintLen],
			Scopes:    scopes,
			CreatedBy: claim.UserID,
			ExpiresAt: input.ExpiresAt,
		})
		return err
	}, func() *domain.AuditEntry {
		metadata := serviceTokenAuditValue(created)
		if created.ExpiresAt != nil {
			metadata["expires_at"] = created.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return projectAudit(domain.AuditServiceTokenCreated, projectID, domain.AuditTargetServiceToken, created.ID.Base36(), metadata)
	})
	if err != nil {
		return nil, "", err
	}
	return created, plaintext, nil
}

func (s *ServiceToken) List(ctx context.Context, projectID snow.ID) ([]*domain.ServiceToken, error) {
	if !s.perm.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	return s.repo.ListByProject(ctx, projectID)
}

// Revoke disables a token for good. Revoking an already revoked token is a 404.
func (s *ServiceToken) Revoke(ctx context.Context, projectID, id snow.ID) (*domain.ServiceToken, error) {
	if !s.perm.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	var revoked *domain.ServiceToken
	err := auditedWrite(ctx, s.audit, func(ctx context.Context) error {
		var err error
		revoked, err = s.repo.Revoke(ctx, projectID, id, s.now())
		if domain.IsErrorNotFound(err) {
			return domain.NewErrorNotFound("service token not found")
		}
		return err
	}, func() *domain.AuditEntry {
		return projectAudit(domain.AuditServiceTokenRevoked, projectID, domain.AuditTargetServiceToken, revoked.ID.Base36(), serviceTokenAuditValue(revoked))
	})
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

// Authenticate resolves a service token for a request on the project named by
// orgSlug/projectSlug that needs scope. It returns the claims of the admin who
// created the token, tagged with the token id. Unknown, revoked or expired
// tokens and inactive creators are 401; a token used outside its project or
// scopes, or whose creator is no longer a project admin, is 403.
func (s *ServiceToken) Authenticate(ctx context.Context, plaintext, orgSlug, projectSlug, scope string) (*domain.Claims, error) {
	unauthorized := domain.NewErrorUnauthorized("unauthorized access, token not valid")
	if !strings.HasPrefix(plaintext, domain.ServiceTokenPrefix) {
		return nil, unauthorized
	}
	token, err := s.repo.GetByHash(ctx, hashServiceToken(plaintext))
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, unauthorized
		}
		return nil, err
	}
	now := s.now()
	if !token.Active(now) {
		return nil, unauthorized
	}
	_, project, err := s.projects.ResolveBySlug(ctx, orgSlug, projectSlug)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorForbidden("service token is not valid for this project")
		}
		return nil, err
	}
	if project.ID != token.ProjectID {
		return nil, domain.NewErrorForbidden("service token is not valid for this project")
	}
	if !token.HasScope(scope) {
		return nil, domain.NewErrorForbidden(fmt.Sprintf("service token lacks the %q scope", scope))
	}
	user, err := s.users.GetByID(ctx, token.CreatedBy)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, unauthorized
		}
		return nil, err
	}
	if user.Deleted || !user.EmailVerified {
		return nil, unauthorized
	}
	claims := domain.Claims{
		UserID:         user.ID,
		IsSuperAdmin:   user.IsSuperAdmin,
		IsAdmin:        user.IsAdmin,
		OrgID:          []snow.ID{},
		ServiceTokenID: token.ID,
	}
	if !s.perm.AdminHasProject(domain.ContextWithClaim(ctx, claims), token.ProjectID) {
		return nil, domain.NewErrorForbidden("the service token creator is no longer a project admin")
	}
	if token.LastUsedAt == nil || now.Sub(*token.LastUsedAt) >= serviceTokenTouchEvery {
		_ = s.repo.TouchLastUsed(ctx, token.ID, now)
	}
	return &claims, nil
}

func normalizeServiceTokenScopes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, domain.NewErrorUser("at least one scope is required")
	}
	seen := map[string]bool{}
	for _, entry := range raw {
		scope := strings.TrimSpace(entry)
		if !domain.IsValidServiceTokenScope(scope) {
			return nil, domain.NewErrorUser(fmt.Sprintf("unknown scope %q", scope))
		}
		seen[scope] = true
	}
	scopes := make([]string, 0, len(seen))
	for _, scope := range domain.ServiceTokenScopes {
		if seen[scope] {
			scopes = append(scopes, scope)
		}
	}
	return scopes, nil
}

func serviceTokenAuditValue(token *domain.ServiceToken) map[string]any {
	return map[string]any{
		"name":   token.Name,
		"scopes": token.Scopes,
		"hint":   token.Hint,
	}
}

func generateServiceToken() (string, error) {
	buf := make([]byte, serviceTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return domain.ServiceTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashServiceToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
