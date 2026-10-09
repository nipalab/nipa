// Package mail delivers transactional email through a pluggable sender:
// SMTP (any provider with an SMTP endpoint), the SendGrid v3 REST API, a
// generic HTTP JSON sender that adapts to any REST provider via a body
// template, or the server log for development.
package mail

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Sender kinds accepted by NewFromConfig.
const (
	SenderOff      = "off"
	SenderLog      = "log"
	SenderSMTP     = "smtp"
	SenderSendGrid = "sendgrid"
	SenderHTTP     = "http"
)

// DefaultTimeout bounds one delivery attempt when Config.Timeout is zero.
const DefaultTimeout = 10 * time.Second

// UserAgent identifies nipad to HTTP email providers.
const UserAgent = "Nipa/1.0"

// Config selects and configures the sender. An empty Sender means SenderOff,
// in which case NewFromConfig returns a nil Sender.
type Config struct {
	Sender  string
	From    string
	ReplyTo string
	Timeout time.Duration

	SMTP     SMTPConfig
	SendGrid SendGridConfig
	HTTP     HTTPConfig
}

// SMTPConfig configures the SMTP sender. TLS is "starttls" (default), "ssl"
// for implicit TLS or "none". Port defaults to 587, 465 and 25 respectively.
type SMTPConfig struct {
	Host               string
	Port               int
	Username           string
	Password           string
	TLS                string
	InsecureSkipVerify bool
}

// SendGridConfig configures the SendGrid v3 API sender. Endpoint defaults to
// https://api.sendgrid.com.
type SendGridConfig struct {
	APIKey   string
	Endpoint string
}

// HTTPConfig configures the generic REST sender. HeadersJSON is a JSON object
// of extra request headers. BodyTemplate is a Go text/template rendered per
// recipient with a json helper; it defaults to a provider-neutral JSON body.
type HTTPConfig struct {
	Endpoint     string
	Method       string
	HeadersJSON  string
	ContentType  string
	BodyTemplate string
}

// NewFromConfig builds the configured sender. SenderOff returns (nil, nil):
// callers leave the notification seam unwired.
func NewFromConfig(cfg Config) (Sender, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Sender)) {
	case "", SenderOff:
		return nil, nil
	case SenderLog:
		return &LogSender{}, nil
	case SenderSMTP:
		return newSMTPSender(cfg.SMTP, cfg.From, cfg.ReplyTo, timeout)
	case SenderSendGrid:
		return newSendGridSender(cfg.SendGrid, cfg.From, cfg.ReplyTo, timeout)
	case SenderHTTP:
		return newHTTPSender(cfg.HTTP, cfg.From, cfg.ReplyTo, timeout)
	default:
		return nil, fmt.Errorf("unknown email sender %q", cfg.Sender)
	}
}

func parseHeadersJSON(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(trimmed), &headers); err != nil {
		return nil, fmt.Errorf("invalid email http headers: %w", err)
	}
	return headers, nil
}
