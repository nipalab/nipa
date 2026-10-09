package config

import (
	"time"

	"github.com/nipalab/nipa/internal/mail"
)

// MailSenderConfig maps the EMAIL_* settings onto the mail sender factory
// config. Both nipad editions share this mapping.
func (c *Config) MailSenderConfig() mail.Config {
	return mail.Config{
		Sender:  c.EmailSender,
		From:    c.EmailFrom,
		ReplyTo: c.EmailReplyTo,
		Timeout: time.Duration(c.EmailTimeoutSeconds) * time.Second,
		SMTP: mail.SMTPConfig{
			Host:               c.EmailSMTPHost,
			Port:               c.EmailSMTPPort,
			Username:           c.EmailSMTPUsername,
			Password:           c.EmailSMTPPassword,
			TLS:                c.EmailSMTPTLS,
			InsecureSkipVerify: c.EmailSMTPInsecureTLS,
		},
		SendGrid: mail.SendGridConfig{
			APIKey:   c.EmailSendGridAPIKey,
			Endpoint: c.EmailSendGridEndpoint,
		},
		HTTP: mail.HTTPConfig{
			Endpoint:     c.EmailHTTPEndpoint,
			Method:       c.EmailHTTPMethod,
			HeadersJSON:  c.EmailHTTPHeaders,
			ContentType:  c.EmailHTTPContentType,
			BodyTemplate: c.EmailHTTPBodyTemplate,
		},
	}
}
