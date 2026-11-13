package handler

import (
	"encoding/base32"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	apphttp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/totp"
)

func enrollHandlerTOTP(t *testing.T, env *handlerTestEnv, claims *domain.Claims) ([]byte, []string) {
	t.Helper()
	start := &fakeAppContext{claims: claims, body: []byte(`{"password":"pw"}`)}
	env.handler.StartMyTOTP(start)
	require.Equal(t, http.StatusOK, start.statusCode)
	enrollment := start.response.(model.TOTPEnrollmentResponse)
	require.Contains(t, enrollment.URI, "otpauth://totp/")
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	require.NoError(t, err)

	confirm := &fakeAppContext{claims: claims, body: []byte(`{"code":"` + totp.Code(secret, totp.Step(time.Now())) + `"}`)}
	env.handler.ConfirmMyTOTP(confirm)
	require.Equal(t, http.StatusOK, confirm.statusCode, confirm.response)
	codes := confirm.response.(model.RecoveryCodesResponse).RecoveryCodes
	require.Len(t, codes, totp.RecoveryCodeCount)
	return secret, codes
}

func TestHandler_MFAEnrollAndLogin(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}

	status := &fakeAppContext{claims: claims}
	env.handler.GetMyMFA(status)
	require.Equal(t, http.StatusOK, status.statusCode)
	require.Equal(t, model.MFAStatusResponse{Available: true}, status.response)

	_, codes := enrollHandlerTOTP(t, env, claims)

	status = &fakeAppContext{claims: claims}
	env.handler.GetMyMFA(status)
	got := status.response.(model.MFAStatusResponse)
	require.True(t, got.TOTP.Enabled)
	require.NotNil(t, got.TOTP.ConfirmedAt)
	require.EqualValues(t, totp.RecoveryCodeCount, got.RecoveryCodesLeft)

	login := &fakeAppContext{body: []byte(`{"email":"alice@example.com","password":"pw"}`)}
	env.handler.AuthLogin(login)
	require.Equal(t, http.StatusOK, login.statusCode)
	challenge, ok := login.response.(model.MFAChallengeResponse)
	require.True(t, ok)
	require.True(t, challenge.MFARequired)
	require.NotEmpty(t, challenge.MFAChallenge)
	require.Equal(t, 300, challenge.ExpiresIn)
	require.Empty(t, login.cookies, "no session before the second factor")

	wrong := &fakeAppContext{body: []byte(`{"challenge":"` + challenge.MFAChallenge + `","code":"000000"}`)}
	env.handler.AuthVerifyMFA(wrong)
	require.Equal(t, http.StatusBadRequest, wrong.statusCode)

	verify := &fakeAppContext{body: []byte(`{"challenge":"` + challenge.MFAChallenge + `","code":"` + codes[0] + `"}`)}
	env.handler.AuthVerifyMFA(verify)
	require.Equal(t, http.StatusOK, verify.statusCode, verify.response)
	resp := verify.response.(model.LoginResponse)
	require.NotEmpty(t, resp.AccessToken)
	require.NotEmpty(t, verify.cookies[refreshCookieName].Value)
	accessClaims := parseHandlerAccessToken(t, resp.AccessToken, "test-secret")
	require.Equal(t, []string{"password", domain.MFARecoveryCode}, accessClaims.AuthMethods)
}

func TestHandler_MFAManage(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	_, codes := enrollHandlerTOTP(t, env, claims)

	again := &fakeAppContext{claims: claims, body: []byte(`{"password":"pw"}`)}
	env.handler.StartMyTOTP(again)
	require.Equal(t, http.StatusConflict, again.statusCode)

	regen := &fakeAppContext{claims: claims, body: []byte(`{"code":"` + codes[1] + `"}`)}
	env.handler.RegenerateMyRecoveryCodes(regen)
	require.Equal(t, http.StatusOK, regen.statusCode, regen.response)
	fresh := regen.response.(model.RecoveryCodesResponse).RecoveryCodes
	require.Len(t, fresh, totp.RecoveryCodeCount)

	bad := &fakeAppContext{claims: claims, body: []byte(`{"code":"` + codes[2] + `"}`)}
	env.handler.DisableMyTOTP(bad)
	require.Equal(t, http.StatusBadRequest, bad.statusCode, "old codes were replaced")

	disable := &fakeAppContext{claims: claims, body: []byte(`{"code":"` + fresh[0] + `"}`)}
	env.handler.DisableMyTOTP(disable)
	require.Equal(t, http.StatusOK, disable.statusCode, disable.response)

	enrollHandlerTOTP(t, env, claims)
	reset := &fakeAppContext{claims: claims, pathParameters: map[string]string{"user": env.userID.Base36()}}
	env.handler.ResetUserMFA(reset)
	require.Equal(t, http.StatusOK, reset.statusCode, reset.response)

	reset = &fakeAppContext{claims: claims, pathParameters: map[string]string{"user": env.userID.Base36()}}
	env.handler.ResetUserMFA(reset)
	require.Equal(t, http.StatusBadRequest, reset.statusCode)
}

func TestHandler_MFAErrors(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID}

	for name, run := range map[string]func(apphttp.AppContext){
		"verify":     env.handler.AuthVerifyMFA,
		"start":      env.handler.StartMyTOTP,
		"confirm":    env.handler.ConfirmMyTOTP,
		"disable":    env.handler.DisableMyTOTP,
		"regenerate": env.handler.RegenerateMyRecoveryCodes,
	} {
		ctx := &fakeAppContext{claims: claims, body: []byte(`{`)}
		run(ctx)
		require.NotEqual(t, http.StatusOK, ctx.statusCode, name)
	}

	status := &fakeAppContext{}
	env.handler.GetMyMFA(status)
	require.Equal(t, http.StatusUnauthorized, status.statusCode)

	confirm := &fakeAppContext{claims: claims, body: []byte(`{"code":"123456"}`)}
	env.handler.ConfirmMyTOTP(confirm)
	require.Equal(t, http.StatusBadRequest, confirm.statusCode)

	regen := &fakeAppContext{claims: claims, body: []byte(`{"code":"123456"}`)}
	env.handler.RegenerateMyRecoveryCodes(regen)
	require.Equal(t, http.StatusBadRequest, regen.statusCode)

	reset := &fakeAppContext{claims: claims, pathParameters: map[string]string{"user": "!!"}}
	env.handler.ResetUserMFA(reset)
	require.Equal(t, http.StatusBadRequest, reset.statusCode)

	verify := &fakeAppContext{body: []byte(`{"challenge":"nope","code":"123456"}`)}
	env.handler.AuthVerifyMFA(verify)
	require.Equal(t, http.StatusBadRequest, verify.statusCode)
}
