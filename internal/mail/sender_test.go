package mail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewFromConfig_Off(t *testing.T) {
	for _, kind := range []string{"", "off", "OFF", " off "} {
		sender, err := NewFromConfig(Config{Sender: kind})
		require.NoError(t, err)
		require.Nil(t, sender)
	}
}

func TestNewFromConfig_Unknown(t *testing.T) {
	sender, err := NewFromConfig(Config{Sender: "carrier-pigeon"})
	require.Error(t, err)
	require.Nil(t, sender)
	require.Contains(t, err.Error(), "carrier-pigeon")
}

func TestNewFromConfig_Log(t *testing.T) {
	sender, err := NewFromConfig(Config{Sender: "log"})
	require.NoError(t, err)
	require.IsType(t, &LogSender{}, sender)
}

func TestNewFromConfig_SMTP(t *testing.T) {
	sender, err := NewFromConfig(Config{Sender: "smtp", From: "noreply@example.com", SMTP: SMTPConfig{Host: "smtp.example.com"}})
	require.NoError(t, err)
	defaultSender, ok := sender.(*smtpSender)
	require.True(t, ok)
	require.Equal(t, smtpTLSStartTLS, defaultSender.cfg.TLS)
	require.Equal(t, 587, defaultSender.cfg.Port)
	require.Equal(t, DefaultTimeout, defaultSender.timeout)

	ssl, err := NewFromConfig(Config{Sender: "smtp", From: "noreply@example.com", SMTP: SMTPConfig{Host: "smtp.example.com", TLS: "ssl"}})
	require.NoError(t, err)
	require.Equal(t, 465, ssl.(*smtpSender).cfg.Port)

	plain, err := NewFromConfig(Config{Sender: "smtp", From: "noreply@example.com", SMTP: SMTPConfig{Host: "smtp.example.com", TLS: "none"}})
	require.NoError(t, err)
	require.Equal(t, 25, plain.(*smtpSender).cfg.Port)
}

func TestNewFromConfig_SMTPValidation(t *testing.T) {
	_, err := NewFromConfig(Config{Sender: "smtp", From: "noreply@example.com"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "host")

	_, err = NewFromConfig(Config{Sender: "smtp", From: "not-an-address", SMTP: SMTPConfig{Host: "smtp.example.com"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "from")

	_, err = NewFromConfig(Config{Sender: "smtp", From: "noreply@example.com", SMTP: SMTPConfig{Host: "smtp.example.com", TLS: "smoke-signals"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tls")

	_, err = NewFromConfig(Config{Sender: "smtp", From: "noreply@example.com", ReplyTo: "bad", SMTP: SMTPConfig{Host: "smtp.example.com"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reply-to")
}

func TestNewFromConfig_SendGrid(t *testing.T) {
	sender, err := NewFromConfig(Config{Sender: "sendgrid", From: "noreply@example.com", SendGrid: SendGridConfig{APIKey: "key"}})
	require.NoError(t, err)
	sg, ok := sender.(*sendGridSender)
	require.True(t, ok)
	require.Equal(t, defaultSendGridEndpoint, sg.endpoint)

	_, err = NewFromConfig(Config{Sender: "sendgrid", From: "noreply@example.com"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "api key")

	_, err = NewFromConfig(Config{Sender: "sendgrid", SendGrid: SendGridConfig{APIKey: "key"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "from")
}

func TestNewFromConfig_HTTP(t *testing.T) {
	sender, err := NewFromConfig(Config{Sender: "http", From: "noreply@example.com", HTTP: HTTPConfig{Endpoint: "https://mail.example.com/send"}})
	require.NoError(t, err)
	httpSender, ok := sender.(*httpSender)
	require.True(t, ok)
	require.Equal(t, "POST", httpSender.method)
	require.Equal(t, "application/json", httpSender.contentType)

	_, err = NewFromConfig(Config{Sender: "http", From: "noreply@example.com"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "endpoint")

	_, err = NewFromConfig(Config{Sender: "http", From: "noreply@example.com", HTTP: HTTPConfig{Endpoint: "https://x", HeadersJSON: "{bad"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "headers")

	_, err = NewFromConfig(Config{Sender: "http", From: "noreply@example.com", HTTP: HTTPConfig{Endpoint: "https://x", BodyTemplate: "{{.Nope"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "template")
}

func TestParseHeadersJSON(t *testing.T) {
	headers, err := parseHeadersJSON(`{"X-A":"1","X-B":"2"}`)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-A": "1", "X-B": "2"}, headers)

	headers, err = parseHeadersJSON("")
	require.NoError(t, err)
	require.Nil(t, headers)

	_, err = parseHeadersJSON("not json")
	require.Error(t, err)
}

func TestConfigTimeoutDefault(t *testing.T) {
	sender, err := NewFromConfig(Config{Sender: "sendgrid", From: "noreply@example.com", SendGrid: SendGridConfig{APIKey: "key"}, Timeout: 2 * time.Second})
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, sender.(*sendGridSender).client.Timeout)
}
