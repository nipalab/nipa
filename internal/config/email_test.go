package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMailSenderConfig(t *testing.T) {
	cfg := &Config{
		EmailSender:           "smtp",
		EmailFrom:             "Nipa <noreply@example.com>",
		EmailReplyTo:          "support@example.com",
		EmailTimeoutSeconds:   15,
		EmailSMTPHost:         "smtp.example.com",
		EmailSMTPPort:         2525,
		EmailSMTPUsername:     "mailer",
		EmailSMTPPassword:     "mailer-secret",
		EmailSMTPTLS:          "ssl",
		EmailSMTPInsecureTLS:  true,
		EmailSendGridAPIKey:   "sg-key",
		EmailSendGridEndpoint: "https://sendgrid.internal",
		EmailHTTPEndpoint:     "https://mail.example.com/send",
		EmailHTTPMethod:       "PUT",
		EmailHTTPHeaders:      `{"X-Api-Key":"secret"}`,
		EmailHTTPContentType:  "application/json",
		EmailHTTPBodyTemplate: `{"to":{{json .To}}}`,
	}

	mapped := cfg.MailSenderConfig()
	require.Equal(t, "smtp", mapped.Sender)
	require.Equal(t, "Nipa <noreply@example.com>", mapped.From)
	require.Equal(t, "support@example.com", mapped.ReplyTo)
	require.Equal(t, 15*time.Second, mapped.Timeout)
	require.Equal(t, "smtp.example.com", mapped.SMTP.Host)
	require.Equal(t, 2525, mapped.SMTP.Port)
	require.Equal(t, "mailer", mapped.SMTP.Username)
	require.Equal(t, "mailer-secret", mapped.SMTP.Password)
	require.Equal(t, "ssl", mapped.SMTP.TLS)
	require.True(t, mapped.SMTP.InsecureSkipVerify)
	require.Equal(t, "sg-key", mapped.SendGrid.APIKey)
	require.Equal(t, "https://sendgrid.internal", mapped.SendGrid.Endpoint)
	require.Equal(t, "https://mail.example.com/send", mapped.HTTP.Endpoint)
	require.Equal(t, "PUT", mapped.HTTP.Method)
	require.Equal(t, `{"X-Api-Key":"secret"}`, mapped.HTTP.HeadersJSON)
	require.Equal(t, "application/json", mapped.HTTP.ContentType)
	require.Equal(t, `{"to":{{json .To}}}`, mapped.HTTP.BodyTemplate)
}
