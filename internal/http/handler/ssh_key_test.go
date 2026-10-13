package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
)

func testHandlerAuthorizedKey(t *testing.T, comment string) string {
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

func TestHandler_SSHKeyLifecycle(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)
	claims := &domain.Claims{UserID: userID}

	addCtx := &fakeAppContext{
		claims: claims,
		body:   []byte(`{"name":"work laptop","public_key":"` + testHandlerAuthorizedKey(t, "") + `"}`),
	}
	h.AddMySSHKey(addCtx)
	require.Equal(t, http.StatusOK, addCtx.statusCode)

	created, ok := addCtx.response.(model.SSHKeyResponse)
	require.True(t, ok)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "work laptop", created.Name)
	require.Equal(t, "ssh-ed25519", created.KeyType)
	require.True(t, strings.HasPrefix(created.Fingerprint, "SHA256:"))

	listCtx := &fakeAppContext{claims: claims}
	h.ListMySSHKeys(listCtx)
	require.Equal(t, http.StatusOK, listCtx.statusCode)
	keys, ok := listCtx.response.([]model.SSHKeyResponse)
	require.True(t, ok)
	require.Len(t, keys, 1)
	require.Equal(t, created.ID, keys[0].ID)

	removeCtx := &fakeAppContext{claims: claims, pathParameters: map[string]string{"key": created.ID}}
	h.RemoveMySSHKey(removeCtx)
	require.Equal(t, http.StatusOK, removeCtx.statusCode)

	listCtx = &fakeAppContext{claims: claims}
	h.ListMySSHKeys(listCtx)
	keys, ok = listCtx.response.([]model.SSHKeyResponse)
	require.True(t, ok)
	require.Empty(t, keys)
}

func TestHandler_AddMySSHKey_NameFallsBackToComment(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		claims: &domain.Claims{UserID: userID},
		body:   []byte(`{"public_key":"` + testHandlerAuthorizedKey(t, "alice@laptop") + `"}`),
	}
	h.AddMySSHKey(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	created, ok := appCtx.response.(model.SSHKeyResponse)
	require.True(t, ok)
	require.Equal(t, "alice@laptop", created.Name)
	require.Equal(t, "alice@laptop", created.Comment)
}

func TestHandler_AddMySSHKey_InvalidKey(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		claims: &domain.Claims{UserID: userID},
		body:   []byte(`{"public_key":"not a key"}`),
	}
	h.AddMySSHKey(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_AddMySSHKey_Duplicate(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)
	claims := &domain.Claims{UserID: userID}
	line := testHandlerAuthorizedKey(t, "")

	first := &fakeAppContext{claims: claims, body: []byte(`{"public_key":"` + line + `"}`)}
	h.AddMySSHKey(first)
	require.Equal(t, http.StatusOK, first.statusCode)

	second := &fakeAppContext{claims: claims, body: []byte(`{"public_key":"` + line + `"}`)}
	h.AddMySSHKey(second)
	require.Equal(t, http.StatusConflict, second.statusCode)
}

func TestHandler_SSHKey_Unauthenticated(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	listCtx := &fakeAppContext{}
	h.ListMySSHKeys(listCtx)
	require.Equal(t, http.StatusUnauthorized, listCtx.statusCode)

	addCtx := &fakeAppContext{body: []byte(`{"public_key":"x"}`)}
	h.AddMySSHKey(addCtx)
	require.Equal(t, http.StatusUnauthorized, addCtx.statusCode)

	removeCtx := &fakeAppContext{pathParameters: map[string]string{"key": "1"}}
	h.RemoveMySSHKey(removeCtx)
	require.Equal(t, http.StatusUnauthorized, removeCtx.statusCode)
}

func TestHandler_RemoveMySSHKey_InvalidID(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID},
		pathParameters: map[string]string{"key": "!!!"},
	}
	h.RemoveMySSHKey(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_RemoveMySSHKey_NotFound(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		claims:         &domain.Claims{UserID: userID},
		pathParameters: map[string]string{"key": "42"},
	}
	h.RemoveMySSHKey(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}
