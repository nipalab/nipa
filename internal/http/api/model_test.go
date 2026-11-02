package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/stretchr/testify/require"
)

func registeredClaimsForSubject(subject string) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Subject:   subject,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
}

func TestNewAppContext(t *testing.T) {
	req := restful.NewRequest(httptest.NewRequest(http.MethodGet, "/", nil))
	resp := restful.NewResponse(httptest.NewRecorder())

	ctx, err := newAppContext(req, resp)
	require.NoError(t, err)

	appCtx := ctx
	require.Nil(t, appCtx.Claims())
	require.Equal(t, req.Request.Context(), appCtx.Context())
}

func TestAppContext_Claims(t *testing.T) {
	wantClaims := &domain.Claims{UserID: 7}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	restfulReq := restful.NewRequest(req)
	restfulReq.SetAttribute(AttributeClaims, wantClaims)

	ctx, err := newAppContext(restfulReq, restful.NewResponse(httptest.NewRecorder()))
	require.NoError(t, err)

	require.Same(t, wantClaims, ctx.Claims())
}

func TestAppContext_ReadJson(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"email":"a@b.c"}`))
	req.Header.Set("Content-Type", "application/json")
	restfulReq := restful.NewRequest(req)

	ctx, err := newAppContext(restfulReq, restful.NewResponse(httptest.NewRecorder()))
	require.NoError(t, err)

	var body struct {
		Email string `json:"email"`
	}
	require.NoError(t, ctx.ReadJson(&body))
	require.Equal(t, "a@b.c", body.Email)

	err = ctx.ReadJson(&struct{}{})
	require.Error(t, err)
}

func TestAppContext_WriteJson(t *testing.T) {
	recorder := httptest.NewRecorder()
	resp := restful.NewResponse(recorder)

	ctx, err := newAppContext(restful.NewRequest(httptest.NewRequest(http.MethodGet, "/", nil)), resp)
	require.NoError(t, err)

	require.NoError(t, ctx.WriteJson(http.StatusOK, map[string]string{"ok": "yes"}))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"ok":"yes"}`, recorder.Body.String())
}

func TestAppContext_HandleError_DomainError(t *testing.T) {
	recorder := httptest.NewRecorder()

	ctx, err := newAppContext(
		restful.NewRequest(httptest.NewRequest(http.MethodGet, "/", nil)),
		restful.NewResponse(recorder),
	)
	require.NoError(t, err)

	ctx.HandleError(domain.NewErrorUser("refresh token expired"))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.JSONEq(t, `{"error":"refresh token expired"}`, recorder.Body.String())
}

func TestAppContext_HandleError_UnknownError(t *testing.T) {
	recorder := httptest.NewRecorder()

	ctx, err := newAppContext(
		restful.NewRequest(httptest.NewRequest(http.MethodGet, "/", nil)),
		restful.NewResponse(recorder),
	)
	require.NoError(t, err)

	ctx.HandleError(errors.New("something exploded"))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.JSONEq(t, `{"error":"internal unknown error"}`, recorder.Body.String())
}
