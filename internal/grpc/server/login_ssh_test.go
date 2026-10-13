package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/sshkey"
	"github.com/nipalab/nipa/internal/usecase"
)

func newSSHLoginServer(t *testing.T, userRepo *stubUserRepo, authRepo *stubAuthRepo, keyRepo *stubSSHKeyRepo) *nipaServer {
	t.Helper()
	auth := usecase.NewAuth("test-secret", stubPasswordHasher{}, userRepo, authRepo).WithSSHKeys(keyRepo)
	return New(&loginMockContainer{auth: auth})
}

func sshLoginSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return signer
}

func sshLoginSignature(t *testing.T, signer ssh.Signer, nonce string) []byte {
	t.Helper()
	sig, err := signer.Sign(rand.Reader, sshkey.LoginPayload(nonce))
	require.NoError(t, err)
	return ssh.Marshal(sig)
}

func TestBeginLoginSSHKey_Success(t *testing.T) {
	authRepo := &stubAuthRepo{}
	srv := newSSHLoginServer(t, &stubUserRepo{}, authRepo, &stubSSHKeyRepo{})

	resp, err := srv.BeginLoginSSHKey(context.Background(), &pb.BeginLoginSSHKeyRequest{
		Email: "  alice@example.com ",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Nonce)
	require.Equal(t, int32(60), resp.ExpiresIn)
	require.Equal(t, "alice@example.com", authRepo.createdEmail)
	require.Equal(t, resp.Nonce, authRepo.createdNonce)
	require.WithinDuration(t, time.Now().Add(60*time.Second), authRepo.createdExpiry, 5*time.Second)
}

func TestBeginLoginSSHKey_EmptyEmail(t *testing.T) {
	srv := newSSHLoginServer(t, &stubUserRepo{}, &stubAuthRepo{}, &stubSSHKeyRepo{})

	_, err := srv.BeginLoginSSHKey(context.Background(), &pb.BeginLoginSSHKeyRequest{Email: " "})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCompleteLoginSSHKey_Success(t *testing.T) {
	signer := sshLoginSigner(t)
	nonce := "nonce-1"
	user := &domain.User{ID: snow.ID(42), Name: "alice", Email: "alice@example.com", IsAdmin: true}
	authRepo := &stubAuthRepo{
		challenge: &domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     nonce,
			ExpiresAt: time.Now().Add(30 * time.Second),
		},
	}
	keyRepo := &stubSSHKeyRepo{key: &domain.SSHPublicKey{ID: snow.ID(1), UserID: snow.ID(42)}}
	srv := newSSHLoginServer(t, &stubUserRepo{user: user}, authRepo, keyRepo)

	resp, err := srv.CompleteLoginSSHKey(context.Background(), &pb.CompleteLoginSSHKeyRequest{
		Email:     "alice@example.com",
		Nonce:     nonce,
		PublicKey: signer.PublicKey().Marshal(),
		Signature: sshLoginSignature(t, signer, nonce),
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.AccessToken)
	require.NotEmpty(t, resp.RefreshToken)
	require.Equal(t, int32(30*60), resp.ExpiresIn)
}

func TestCompleteLoginSSHKey_UnknownNonce(t *testing.T) {
	authRepo := &stubAuthRepo{consumeErr: domain.NewErrorRecordNotFound()}
	srv := newSSHLoginServer(t, &stubUserRepo{}, authRepo, &stubSSHKeyRepo{})

	_, err := srv.CompleteLoginSSHKey(context.Background(), &pb.CompleteLoginSSHKeyRequest{
		Email:     "alice@example.com",
		Nonce:     "missing",
		PublicKey: []byte("key"),
		Signature: []byte("sig"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCompleteLoginSSHKey_Expired(t *testing.T) {
	authRepo := &stubAuthRepo{
		challenge: &domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     "nonce-1",
			ExpiresAt: time.Now().Add(-time.Second),
		},
	}
	srv := newSSHLoginServer(t, &stubUserRepo{}, authRepo, &stubSSHKeyRepo{})

	_, err := srv.CompleteLoginSSHKey(context.Background(), &pb.CompleteLoginSSHKeyRequest{
		Email:     "alice@example.com",
		Nonce:     "nonce-1",
		PublicKey: []byte("key"),
		Signature: []byte("sig"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCompleteLoginSSHKey_BadSignature(t *testing.T) {
	signer := sshLoginSigner(t)
	nonce := "nonce-1"
	authRepo := &stubAuthRepo{
		challenge: &domain.SSHLoginChallenge{
			Email:     "alice@example.com",
			Nonce:     nonce,
			ExpiresAt: time.Now().Add(30 * time.Second),
		},
	}
	keyRepo := &stubSSHKeyRepo{key: &domain.SSHPublicKey{ID: snow.ID(1), UserID: snow.ID(42)}}
	srv := newSSHLoginServer(t, &stubUserRepo{user: &domain.User{ID: snow.ID(42), Email: "alice@example.com"}}, authRepo, keyRepo)

	sig, err := signer.Sign(rand.Reader, sshkey.LoginPayload("tampered"))
	require.NoError(t, err)

	_, err = srv.CompleteLoginSSHKey(context.Background(), &pb.CompleteLoginSSHKeyRequest{
		Email:     "alice@example.com",
		Nonce:     nonce,
		PublicKey: signer.PublicKey().Marshal(),
		Signature: ssh.Marshal(sig),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCompleteLoginSSHKey_MissingFields(t *testing.T) {
	srv := newSSHLoginServer(t, &stubUserRepo{}, &stubAuthRepo{}, &stubSSHKeyRepo{})

	_, err := srv.CompleteLoginSSHKey(context.Background(), &pb.CompleteLoginSSHKeyRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
