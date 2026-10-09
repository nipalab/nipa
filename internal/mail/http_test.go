package mail

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPSender_Send(t *testing.T) {
	var capturedMethod, capturedContentType, capturedAPIKey, capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedContentType = r.Header.Get("Content-Type")
		capturedAPIKey = r.Header.Get("X-Api-Key")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newHTTPSender(HTTPConfig{
		Endpoint:    server.URL,
		Method:      "PUT",
		HeadersJSON: `{"X-Api-Key":"secret"}`,
		BodyTemplate: `{"to":"{{.ToHeader}}","from":{{json .FromEmail}},` +
			`"subject":{{json .Subject}},"body":{{json .Text}},"id":{{json .MessageID}}}`,
	}, "Nipa <noreply@example.com>", "", 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sender.Close() })

	err = sender.Send(context.Background(), Message{
		To:        []string{"dev@example.com", "ops@example.com"},
		Subject:   "Hi \"there\"",
		Text:      "line one\nline two",
		MessageID: "msg-1@nipa",
	})
	require.NoError(t, err)

	require.Equal(t, "PUT", capturedMethod)
	require.Equal(t, "application/json", capturedContentType)
	require.Equal(t, "secret", capturedAPIKey)
	require.Equal(t, `{"to":"dev@example.com, ops@example.com","from":"noreply@example.com","subject":"Hi \"there\"","body":"line one\nline two","id":"<msg-1@nipa>"}`, capturedBody)
}

func TestHTTPSender_DefaultTemplate(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newHTTPSender(HTTPConfig{Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)
	require.JSONEq(t, `{"from":"noreply@example.com","from_email":"noreply@example.com","to":["dev@example.com"],"subject":"s","text":"b","html":""}`, capturedBody)
}

func TestHTTPSender_Overrides(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newHTTPSender(HTTPConfig{
		Endpoint:     server.URL,
		BodyTemplate: `{"from":{{json .FromEmail}},"reply_to":{{json .ReplyTo}}}`,
	}, "default@example.com", "default-reply@example.com", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		From:    "Override <other@example.com>",
		ReplyTo: "reply@example.com",
		To:      []string{"dev@example.com"},
		Subject: "s",
	})
	require.NoError(t, err)
	require.Equal(t, `{"from":"other@example.com","reply_to":"reply@example.com"}`, capturedBody)
}

func TestHTTPSender_InvalidRecipient(t *testing.T) {
	sender, err := newHTTPSender(HTTPConfig{Endpoint: "https://mail.example.com"}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"bad address"}, Subject: "s"})
	require.Error(t, err)
}

func TestHTTPSender_InvalidEndpoint(t *testing.T) {
	for _, endpoint := range []string{"://bad", "mail.example.com/send", "ftp://mail.example.com/send", "https://"} {
		_, err := newHTTPSender(HTTPConfig{Endpoint: endpoint}, "noreply@example.com", "", 5*time.Second)
		require.Error(t, err, "endpoint %q should be rejected", endpoint)
		require.Contains(t, err.Error(), "endpoint")
	}
}

func TestHTTPSender_EmptyThreadingHeaders(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newHTTPSender(HTTPConfig{
		Endpoint:     server.URL,
		BodyTemplate: `{"message_id":{{json .MessageID}},"in_reply_to":{{json .InReplyTo}},"references":{{json .References}}}`,
	}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)
	require.JSONEq(t, `{"message_id":"","in_reply_to":"","references":null}`, capturedBody)
}

func TestHTTPSender_NormalizedReferences(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender, err := newHTTPSender(HTTPConfig{
		Endpoint:     server.URL,
		BodyTemplate: `{"message_id":{{json .MessageID}},"in_reply_to":{{json .InReplyTo}},"references":{{json .References}}}`,
	}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		To:         []string{"dev@example.com"},
		Subject:    "s",
		Text:       "b",
		MessageID:  "msg-1@nipa",
		InReplyTo:  "root@nipa",
		References: []string{"root@nipa", "<prev@nipa>"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"message_id":"<msg-1@nipa>","in_reply_to":"<root@nipa>","references":["<root@nipa>","<prev@nipa>"]}`, capturedBody)
}

func TestJSONStringError(t *testing.T) {
	_, err := jsonString(make(chan int))
	require.Error(t, err)
}

func TestHTTPSender_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("provider exploded"))
	}))
	t.Cleanup(server.Close)

	sender, err := newHTTPSender(HTTPConfig{Endpoint: server.URL}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
	require.Contains(t, err.Error(), "provider exploded")
}
