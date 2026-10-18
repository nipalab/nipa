package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScalewaySender_Send(t *testing.T) {
	var captured scalewayPayload
	var capturedAuth, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("X-Auth-Token")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newScalewaySender(ScalewayConfig{APIKey: "test-key", ProjectID: "project-1", Endpoint: server.URL}, "Nipa <noreply@example.com>", "", 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sender.Close() })

	err = sender.Send(context.Background(), Message{
		To:        []string{"Dev <dev@example.com>"},
		Subject:   "MR ready",
		Text:      "plain",
		HTML:      "<p>html</p>",
		MessageID: "msg-1@nipa",
		InReplyTo: "root@nipa",
	})
	require.NoError(t, err)

	require.Equal(t, "/transactional-email/v1alpha1/regions/fr-par/emails", capturedPath)
	require.Equal(t, "test-key", capturedAuth)
	require.Equal(t, "noreply@example.com", captured.From.Email)
	require.Equal(t, "Nipa", captured.From.Name)
	require.Len(t, captured.To, 1)
	require.Equal(t, "dev@example.com", captured.To[0].Email)
	require.Equal(t, "Dev", captured.To[0].Name)
	require.Equal(t, "MR ready", captured.Subject)
	require.Equal(t, "plain", captured.Text)
	require.Equal(t, "<p>html</p>", captured.HTML)
	require.Equal(t, "project-1", captured.ProjectID)
	require.Equal(t, "<msg-1@nipa>", headerValue(captured, "Message-ID"))
	require.Equal(t, "<root@nipa>", headerValue(captured, "In-Reply-To"))
}

func TestScalewaySender_PathDimensionOverrides(t *testing.T) {
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newScalewaySender(ScalewayConfig{APIKey: "key", ProjectID: "p", Region: "nl-ams", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)
	require.Equal(t, "/transactional-email/v1alpha1/regions/nl-ams/emails", capturedPath)
}

func TestScalewaySender_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	t.Cleanup(server.Close)

	sender, err := newScalewaySender(ScalewayConfig{APIKey: "test-key", ProjectID: "project-1", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
	require.Contains(t, err.Error(), "invalid token")
}

func TestScalewaySender_RejectsHeaderInjection(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newScalewaySender(ScalewayConfig{APIKey: "key", ProjectID: "project-1", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		To:        []string{"dev@example.com"},
		Subject:   "s",
		Text:      "b",
		InReplyTo: "x>\r\nBcc: attacker@example.com",
	})
	require.Error(t, err)
	require.False(t, called)
}

func TestScalewaySender_InvalidRecipient(t *testing.T) {
	sender, err := newScalewaySender(ScalewayConfig{APIKey: "key", ProjectID: "project-1"}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"bad address"}, Subject: "s"})
	require.Error(t, err)
}

func TestScalewaySender_Overrides(t *testing.T) {
	var captured scalewayPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newScalewaySender(ScalewayConfig{APIKey: "key", ProjectID: "project-1", Endpoint: server.URL}, "default@example.com", "default-reply@example.com", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		From:       "Override <other@example.com>",
		ReplyTo:    "Reply <reply@example.com>",
		To:         []string{"dev@example.com"},
		Subject:    "s",
		Text:       "b",
		References: []string{"root@nipa"},
	})
	require.NoError(t, err)
	require.Equal(t, "other@example.com", captured.From.Email)
	require.Equal(t, "Reply <reply@example.com>", headerValue(captured, "Reply-To"))
	require.Equal(t, "<root@nipa>", headerValue(captured, "References"))
}

func TestScalewaySender_SubjectOnlyFallback(t *testing.T) {
	var captured scalewayPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newScalewaySender(ScalewayConfig{APIKey: "key", ProjectID: "project-1", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "only subject"})
	require.NoError(t, err)
	require.Equal(t, "only subject", captured.Text)
	require.Empty(t, captured.HTML)
}

func headerValue(payload scalewayPayload, key string) string {
	for _, header := range payload.AdditionalHeaders {
		if header.Key == key {
			return header.Value
		}
	}
	return ""
}
