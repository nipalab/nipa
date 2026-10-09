package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	smtpTLSStartTLS = "starttls"
	smtpTLSSSL      = "ssl"
	smtpTLSNone     = "none"
)

type smtpSender struct {
	cfg     SMTPConfig
	from    address
	replyTo *address
	timeout time.Duration
	dialer  *net.Dialer
}

func newSMTPSender(cfg SMTPConfig, from, replyTo string, timeout time.Duration) (*smtpSender, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("smtp sender: host is required")
	}
	parsedFrom, err := parseAddress(from, "from")
	if err != nil {
		return nil, fmt.Errorf("smtp sender: %w", err)
	}
	var parsedReplyTo *address
	if strings.TrimSpace(replyTo) != "" {
		parsed, err := parseAddress(replyTo, "reply-to")
		if err != nil {
			return nil, fmt.Errorf("smtp sender: %w", err)
		}
		parsedReplyTo = &parsed
	}
	cfg.TLS = strings.ToLower(strings.TrimSpace(cfg.TLS))
	switch cfg.TLS {
	case "":
		cfg.TLS = smtpTLSStartTLS
	case smtpTLSStartTLS, smtpTLSSSL, smtpTLSNone:
	default:
		return nil, fmt.Errorf("smtp sender: unknown tls mode %q", cfg.TLS)
	}
	if cfg.Port == 0 {
		cfg.Port = defaultSMTPPort(cfg.TLS)
	}
	return &smtpSender{
		cfg:     cfg,
		from:    parsedFrom,
		replyTo: parsedReplyTo,
		timeout: timeout,
		dialer:  &net.Dialer{Timeout: timeout},
	}, nil
}

func defaultSMTPPort(mode string) int {
	switch mode {
	case smtpTLSSSL:
		return 465
	case smtpTLSNone:
		return 25
	default:
		return 587
	}
}

func (s *smtpSender) Send(ctx context.Context, msg Message) error {
	from, err := resolveFrom(msg, s.from)
	if err != nil {
		return err
	}
	to, err := parseAddresses(msg.To, "to")
	if err != nil {
		return err
	}
	replyTo, err := resolveReplyTo(msg, s.replyTo)
	if err != nil {
		return err
	}
	body, err := mimeBytes(msg, from, to, replyTo, time.Now())
	if err != nil {
		return err
	}

	conn, err := s.dial(ctx)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.timeout))
	}

	client, err := smtp.NewClient(conn, s.cfg.Host) //NOSONAR
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if s.cfg.TLS == smtpTLSStartTLS {
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from.Addr); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, addr := range to {
		if err := client.Rcpt(addr.Addr); err != nil {
			return fmt.Errorf("smtp rcpt to %s: %w", addr.Addr, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func (s *smtpSender) dial(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	if s.cfg.TLS == smtpTLSSSL {
		return tls.DialWithDialer(s.dialer, "tcp", addr, s.tlsConfig())
	}
	return s.dialer.DialContext(ctx, "tcp", addr)
}

func (s *smtpSender) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         s.cfg.Host,
		InsecureSkipVerify: s.cfg.InsecureSkipVerify, //NOSONAR
	}
}

func (s *smtpSender) Close() error { return nil }
