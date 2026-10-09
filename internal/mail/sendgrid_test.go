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

func TestSendGridSender_Send(t *testing.T) {
	var captured sendGridPayload
	var capturedAuth, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	sender, err := newSendGridSender(SendGridConfig{APIKey: "test-key", Endpoint: server.URL}, "Nipa <noreply@example.com>", "", 5*time.Second)
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

	require.Equal(t, "/v3/mail/send", capturedPath)
	require.Equal(t, "Bearer test-key", capturedAuth)
	require.Len(t, captured.Personalizations, 1)
	require.Equal(t, "dev@example.com", captured.Personalizations[0].To[0].Email)
	require.Equal(t, "Dev", captured.Personalizations[0].To[0].Name)
	require.Equal(t, "<msg-1@nipa>", captured.Personalizations[0].Headers["Message-ID"])
	require.Equal(t, "<root@nipa>", captured.Personalizations[0].Headers["In-Reply-To"])
	require.Equal(t, "noreply@example.com", captured.From.Email)
	require.Equal(t, "Nipa", captured.From.Name)
	require.Equal(t, "MR ready", captured.Subject)
	require.Len(t, captured.Content, 2)
	require.Equal(t, "text/plain", captured.Content[0].Type)
	require.Equal(t, "text/html", captured.Content[1].Type)
}

func TestSendGridSender_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"message":"bad key"}]}`))
	}))
	t.Cleanup(server.Close)

	sender, err := newSendGridSender(SendGridConfig{APIKey: "test-key", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
	require.Contains(t, err.Error(), "bad key")
}

func TestSendGridSender_RejectsHeaderInjection(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	sender, err := newSendGridSender(SendGridConfig{APIKey: "key", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
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

func TestSendGridSender_InvalidRecipient(t *testing.T) {
	sender, err := newSendGridSender(SendGridConfig{APIKey: "key"}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"bad address"}, Subject: "s"})
	require.Error(t, err)
}

func TestSendGridSender_Overrides(t *testing.T) {
	var captured sendGridPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	sender, err := newSendGridSender(SendGridConfig{APIKey: "key", Endpoint: server.URL}, "default@example.com", "default-reply@example.com", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		From:       "Override <other@example.com>",
		ReplyTo:    "reply@example.com",
		To:         []string{"dev@example.com"},
		Subject:    "s",
		Text:       "b",
		References: []string{"root@nipa"},
	})
	require.NoError(t, err)
	require.Equal(t, "other@example.com", captured.From.Email)
	require.Equal(t, "reply@example.com", captured.ReplyTo.Email)
	require.Equal(t, "<root@nipa>", captured.Personalizations[0].Headers["References"])
}

func TestSendGridSender_SubjectOnlyFallback(t *testing.T) {
	var captured sendGridPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	sender, err := newSendGridSender(SendGridConfig{APIKey: "test-key", Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "only subject"})
	require.NoError(t, err)
	require.Len(t, captured.Content, 1)
	require.Equal(t, "only subject", captured.Content[0].Value)
}
