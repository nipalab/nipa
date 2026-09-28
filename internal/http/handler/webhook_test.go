package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func TestHandler_WebhookCRUD(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default"}

	appCtx := adminAppCtx(env.userID, params, `{"name":"ci","url":"https://example.com/hook","events":["push","mr.created"],"path_prefix":"assets","is_active":true}`)
	env.handler.CreateWebhook(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	created, ok := appCtx.response.(model.WebhookResponse)
	require.True(t, ok)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "ci", created.Name)
	require.Equal(t, []string{domain.WebhookEventMRCreated, domain.WebhookEventPush}, created.Events)
	require.Equal(t, "assets", created.PathPrefix)
	require.True(t, created.IsActive)
	require.Len(t, created.Secret, 64)

	appCtx = adminAppCtx(env.userID, params, "")
	env.handler.ListWebhooks(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	list := appCtx.response.([]model.WebhookResponse)
	require.Len(t, list, 1)
	require.Empty(t, list[0].Secret, "list never exposes the secret")

	idParams := map[string]string{"org": "default", "project": "default", "id": created.ID}
	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.GetWebhook(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	got := appCtx.response.(model.WebhookResponse)
	require.Equal(t, created.ID, got.ID)
	require.Empty(t, got.Secret, "get never exposes the secret")

	appCtx = adminAppCtx(env.userID, idParams, `{"name":"ci-renamed","is_active":false}`)
	env.handler.UpdateWebhook(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	updated := appCtx.response.(model.WebhookResponse)
	require.Equal(t, "ci-renamed", updated.Name)
	require.False(t, updated.IsActive)
	require.Equal(t, []string{domain.WebhookEventMRCreated, domain.WebhookEventPush}, updated.Events)

	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.RotateWebhookSecret(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	rotated := appCtx.response.(model.WebhookResponse)
	require.Len(t, rotated.Secret, 64)
	require.NotEqual(t, created.Secret, rotated.Secret)

	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.DeleteWebhook(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, params, "")
	env.handler.ListWebhooks(appCtx)
	require.Empty(t, appCtx.response.([]model.WebhookResponse))

	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.GetWebhook(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_WebhookValidationAndAccess(t *testing.T) {
	env := newHandlerTestEnv(t)
	regular := &domain.Claims{UserID: env.userID}
	params := map[string]string{"org": "default", "project": "default"}
	idParams := map[string]string{"org": "default", "project": "default", "id": "1"}

	for _, body := range []string{
		`{"url":"not-a-url","events":["push"]}`,
		`{"url":"https://example.com/hook"}`,
		`{"url":"https://example.com/hook","events":["unknown.event"]}`,
		`{"url":"https://example.com/hook","events":["push"],"path_prefix":"../etc"}`,
	} {
		appCtx := adminAppCtx(env.userID, params, body)
		env.handler.CreateWebhook(appCtx)
		require.Equal(t, http.StatusBadRequest, appCtx.statusCode, body)
	}

	appCtx := adminAppCtx(env.userID, params, `{"url":`)
	env.handler.CreateWebhook(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	for _, run := range []func(httpApp.AppContext){
		env.handler.GetWebhook,
		env.handler.UpdateWebhook,
		env.handler.DeleteWebhook,
		env.handler.RotateWebhookSecret,
	} {
		appCtx := adminAppCtx(env.userID, map[string]string{"org": "default", "project": "default", "id": "!!!"}, "")
		run(appCtx)
		require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
	}

	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.GetWebhook(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, idParams, `{"name":`)
	env.handler.UpdateWebhook(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "missing", "project": "default"}, "")
	env.handler.ListWebhooks(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	createBody := `{"url":"https://example.com/hook","events":["push"]}`
	for _, tc := range []struct {
		name   string
		run    func(httpApp.AppContext)
		params map[string]string
		body   string
	}{
		{name: "list", run: env.handler.ListWebhooks, params: params},
		{name: "create", run: env.handler.CreateWebhook, params: params, body: createBody},
		{name: "get", run: env.handler.GetWebhook, params: idParams},
		{name: "update", run: env.handler.UpdateWebhook, params: idParams, body: createBody},
		{name: "delete", run: env.handler.DeleteWebhook, params: idParams},
		{name: "rotate", run: env.handler.RotateWebhookSecret, params: idParams},
	} {
		appCtx := &fakeAppContext{claims: regular, pathParameters: tc.params}
		if tc.body != "" {
			appCtx.body = []byte(tc.body)
		}
		tc.run(appCtx)
		require.Equal(t, http.StatusForbidden, appCtx.statusCode, tc.name)
	}
}

func TestHandler_WebhookDeliveryEndpoints(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default"}

	appCtx := adminAppCtx(env.userID, params, `{"url":"https://example.com/hook","events":["push"]}`)
	env.handler.CreateWebhook(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	hook := appCtx.response.(model.WebhookResponse)

	idParams := map[string]string{"org": "default", "project": "default", "id": hook.ID}
	deliveryParams := map[string]string{"org": "default", "project": "default", "id": hook.ID, "deliveryId": "1"}

	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.TestWebhook(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	delivery := appCtx.response.(model.WebhookDeliveryResponse)
	require.NotEmpty(t, delivery.ID)
	require.Equal(t, domain.WebhookEventPing, delivery.EventType)
	require.Equal(t, domain.WebhookDeliveryPending, delivery.State)
	require.NotNil(t, delivery.NextRetryAt, "an immediate attempt is scheduled")

	appCtx = adminAppCtx(env.userID, idParams, "")
	env.handler.ListWebhookDeliveries(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	deliveries := appCtx.response.([]model.WebhookDeliveryResponse)
	require.Len(t, deliveries, 1)
	require.Equal(t, delivery.ID, deliveries[0].ID)

	deliveryParams["deliveryId"] = delivery.ID
	appCtx = adminAppCtx(env.userID, deliveryParams, "")
	env.handler.RedeliverWebhookDelivery(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	requeued := appCtx.response.(model.WebhookDeliveryResponse)
	require.Equal(t, delivery.ID, requeued.ID)
	require.Equal(t, domain.WebhookDeliveryPending, requeued.State)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "default", "id": hook.ID, "deliveryId": "999"}, "")
	env.handler.RedeliverWebhookDelivery(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "default", "id": "999"}, "")
	env.handler.TestWebhook(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	pagedParams := map[string]string{"org": "default", "project": "default", "id": hook.ID}
	appCtx = &fakeAppContext{
		claims:          &domain.Claims{UserID: env.userID, IsAdmin: true},
		pathParameters:  pagedParams,
		queryParameters: map[string]string{"limit": "abc"},
	}
	env.handler.ListWebhookDeliveries(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:          &domain.Claims{UserID: env.userID, IsAdmin: true},
		pathParameters:  pagedParams,
		queryParameters: map[string]string{"limit": "1", "offset": "0"},
	}
	env.handler.ListWebhookDeliveries(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Len(t, appCtx.response.([]model.WebhookDeliveryResponse), 1)
}

func TestHandler_WebhookDeliveryAccessDenied(t *testing.T) {
	env := newHandlerTestEnv(t)
	regular := &domain.Claims{UserID: env.userID}
	idParams := map[string]string{"org": "default", "project": "default", "id": "1"}
	deliveryParams := map[string]string{"org": "default", "project": "default", "id": "1", "deliveryId": "1"}

	for _, tc := range []struct {
		name   string
		run    func(httpApp.AppContext)
		params map[string]string
	}{
		{name: "test", run: env.handler.TestWebhook, params: idParams},
		{name: "deliveries", run: env.handler.ListWebhookDeliveries, params: idParams},
		{name: "redeliver", run: env.handler.RedeliverWebhookDelivery, params: deliveryParams},
	} {
		appCtx := &fakeAppContext{claims: regular, pathParameters: tc.params}
		tc.run(appCtx)
		require.Equal(t, http.StatusForbidden, appCtx.statusCode, tc.name)
	}
}

func TestHandler_WebhookDeliveryContextError(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default", "id": "1", "deliveryId": "1"}

	tests := []struct {
		name string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "test", run: env.handler.TestWebhook},
		{name: "deliveries", run: env.handler.ListWebhookDeliveries},
		{name: "redeliver", run: env.handler.RedeliverWebhookDelivery},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, params, "")
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}

func TestHandler_WebhookContextError(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default", "id": "1"}

	tests := []struct {
		name string
		body string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "list", run: env.handler.ListWebhooks},
		{name: "create", body: `{"url":"https://example.com/hook","events":["push"]}`, run: env.handler.CreateWebhook},
		{name: "get", run: env.handler.GetWebhook},
		{name: "update", body: `{}`, run: env.handler.UpdateWebhook},
		{name: "delete", run: env.handler.DeleteWebhook},
		{name: "rotate", run: env.handler.RotateWebhookSecret},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}
