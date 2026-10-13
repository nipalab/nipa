package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=ssh_key_mock_test.go -package=usecase

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/sshkey"
)

const (
	maxSSHKeysPerUser  = 10
	maxSSHKeyNameRunes = 100
)

type sshKeyRepository interface {
	Create(ctx context.Context, key domain.SSHPublicKey) (*domain.SSHPublicKey, error)
	GetByID(ctx context.Context, id snow.ID) (*domain.SSHPublicKey, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*domain.SSHPublicKey, error)
	ListByUser(ctx context.Context, userID snow.ID) ([]*domain.SSHPublicKey, error)
	CountByUser(ctx context.Context, userID snow.ID) (int, error)
	Delete(ctx context.Context, userID, id snow.ID) error
}

type SSHKey struct {
	snowNode snow.Node
	repo     sshKeyRepository
}

func NewSSHKey(snowNode snow.Node, repo sshKeyRepository) *SSHKey {
	return &SSHKey{
		snowNode: snowNode,
		repo:     repo,
	}
}

func (s *SSHKey) Add(ctx context.Context, userID snow.ID, name, publicKey string) (*domain.SSHPublicKey, error) {
	if err := requireSelfOrAdmin(ctx, userID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxSSHKeyNameRunes {
		return nil, domain.NewErrorUser(fmt.Sprintf("SSH key name must be at most %d characters", maxSSHKeyNameRunes))
	}
	parsed, err := sshkey.ParseAuthorizedKey(publicKey)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	if name == "" {
		name = truncateRunes(parsed.Comment, maxSSHKeyNameRunes)
	}
	if existing, err := s.repo.GetByFingerprint(ctx, parsed.Fingerprint); err == nil {
		if existing.UserID == userID {
			return nil, domain.NewErrorConflict("key already added")
		}
		return nil, domain.NewErrorConflict("key already registered by another user")
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	count, err := s.repo.CountByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if count >= maxSSHKeysPerUser {
		return nil, domain.NewErrorUser(fmt.Sprintf("at most %d SSH keys are allowed per user", maxSSHKeysPerUser))
	}
	return s.repo.Create(ctx, domain.SSHPublicKey{
		ID:          s.snowNode.Generate(),
		UserID:      userID,
		Name:        name,
		KeyType:     parsed.Type,
		KeyBlob:     parsed.Blob,
		Fingerprint: parsed.Fingerprint,
		Comment:     parsed.Comment,
	})
}

func (s *SSHKey) List(ctx context.Context, userID snow.ID) ([]*domain.SSHPublicKey, error) {
	if err := requireSelfOrAdmin(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListByUser(ctx, userID)
}

func (s *SSHKey) Delete(ctx context.Context, userID, keyID snow.ID) error {
	if err := requireSelfOrAdmin(ctx, userID); err != nil {
		return err
	}
	key, err := s.repo.GetByID(ctx, keyID)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return domain.NewErrorNotFound("SSH key not found")
		}
		return err
	}
	if key.UserID != userID {
		return domain.NewErrorNotFound("SSH key not found")
	}
	return s.repo.Delete(ctx, userID, keyID)
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func requireSelfOrAdmin(ctx context.Context, userID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.UserID == userID || claim.IsAdmin || claim.IsSuperAdmin {
		return nil
	}
	return domain.NewErrorNoPermission()
}
