package usecase

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func testSSHKeyLine(t *testing.T, comment string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if comment != "" {
		line += " " + comment
	}
	return line
}

func sshKeyClaimCtx(userID snow.ID, admin bool) context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsAdmin: admin})
}

func TestSSHKey_Add_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	ctx := sshKeyClaimCtx(7, false)
	line := testSSHKeyLine(t, "laptop")
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	require.NoError(t, err)

	repo.EXPECT().
		GetByFingerprint(gomock.Any(), ssh.FingerprintSHA256(parsed)).
		Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().
		CountByUser(gomock.Any(), snow.ID(7)).
		Return(2, nil)

	var captured domain.SSHPublicKey
	repo.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, key domain.SSHPublicKey) (*domain.SSHPublicKey, error) {
			captured = key
			return &key, nil
		})

	got, err := uc.Add(ctx, 7, "work laptop", line)
	require.NoError(t, err)
	require.NotZero(t, got.ID)
	require.Equal(t, snow.ID(7), got.UserID)
	require.Equal(t, "work laptop", got.Name)
	require.Equal(t, "ssh-ed25519", got.KeyType)
	require.Equal(t, ssh.FingerprintSHA256(parsed), got.Fingerprint)
	require.Equal(t, "laptop", got.Comment)
	require.NotEmpty(t, got.KeyBlob)
	require.Equal(t, got, &captured)
}

func TestSSHKey_Add_NameFallsBackToComment(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().
		CountByUser(gomock.Any(), snow.ID(7)).
		Return(0, nil)

	var captured domain.SSHPublicKey
	repo.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, key domain.SSHPublicKey) (*domain.SSHPublicKey, error) {
			captured = key
			return &key, nil
		})

	got, err := uc.Add(sshKeyClaimCtx(7, false), 7, "   ", testSSHKeyLine(t, "alice@laptop"))
	require.NoError(t, err)
	require.Equal(t, "alice@laptop", got.Name)
	require.Equal(t, "alice@laptop", captured.Name)
}

func TestSSHKey_Add_NameTooLong(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, strings.Repeat("x", maxSSHKeyNameRunes+1), testSSHKeyLine(t, ""))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Contains(t, domErr.Message, "at most 100 characters")
}

func TestSSHKey_Add_InvalidKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, "", "not a key")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Contains(t, domErr.Message, "invalid public key")
}

func TestSSHKey_Add_WeakRSAKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, "", "ssh-rsa AAAA")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestSSHKey_Add_DuplicateOwn(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(&domain.SSHPublicKey{ID: 1, UserID: 7}, nil)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, "", testSSHKeyLine(t, ""))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "key already added", domErr.Message)
}

func TestSSHKey_Add_DuplicateOtherUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(&domain.SSHPublicKey{ID: 1, UserID: 8}, nil)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, "", testSSHKeyLine(t, ""))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "key already registered by another user", domErr.Message)
}

func TestSSHKey_Add_FingerprintLookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	wantErr := errors.New("db down")
	repo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(nil, wantErr)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, "", testSSHKeyLine(t, ""))
	require.ErrorIs(t, err, wantErr)
}

func TestSSHKey_Add_LimitReached(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().
		CountByUser(gomock.Any(), snow.ID(7)).
		Return(maxSSHKeysPerUser, nil)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 7, "", testSSHKeyLine(t, ""))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Contains(t, domErr.Message, "at most 10 SSH keys")
}

func TestSSHKey_Add_NoClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	_, err := uc.Add(context.Background(), 7, "", testSSHKeyLine(t, ""))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestSSHKey_Add_OtherUserForbidden(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	_, err := uc.Add(sshKeyClaimCtx(7, false), 8, "", testSSHKeyLine(t, ""))

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestSSHKey_Add_AdminForOtherUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByFingerprint(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().
		CountByUser(gomock.Any(), snow.ID(8)).
		Return(0, nil)
	repo.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, key domain.SSHPublicKey) (*domain.SSHPublicKey, error) {
			return &key, nil
		})

	got, err := uc.Add(sshKeyClaimCtx(7, true), 8, "", testSSHKeyLine(t, ""))
	require.NoError(t, err)
	require.Equal(t, snow.ID(8), got.UserID)
}

func TestSSHKey_List_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	want := []*domain.SSHPublicKey{{ID: 1, UserID: 7, Fingerprint: "SHA256:abc"}}
	repo.EXPECT().
		ListByUser(gomock.Any(), snow.ID(7)).
		Return(want, nil)

	got, err := uc.List(sshKeyClaimCtx(7, false), 7)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSSHKey_List_OtherUserForbidden(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	_, err := uc.List(sshKeyClaimCtx(7, false), 8)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestSSHKey_Delete_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByID(gomock.Any(), snow.ID(3)).
		Return(&domain.SSHPublicKey{ID: 3, UserID: 7}, nil)
	repo.EXPECT().
		Delete(gomock.Any(), snow.ID(7), snow.ID(3)).
		Return(nil)

	require.NoError(t, uc.Delete(sshKeyClaimCtx(7, false), 7, 3))
}

func TestSSHKey_Delete_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByID(gomock.Any(), snow.ID(3)).
		Return(nil, domain.NewErrorRecordNotFound())

	err := uc.Delete(sshKeyClaimCtx(7, false), 7, 3)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "SSH key not found", domErr.Message)
}

func TestSSHKey_Delete_ScopedToUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	repo.EXPECT().
		GetByID(gomock.Any(), snow.ID(3)).
		Return(&domain.SSHPublicKey{ID: 3, UserID: 9}, nil)

	err := uc.Delete(sshKeyClaimCtx(7, true), 8, 3)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestSSHKey_Delete_NoClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocksshKeyRepository(ctrl)
	uc := NewSSHKey(newTestBranchNode(t), repo)

	err := uc.Delete(context.Background(), 7, 3)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}
