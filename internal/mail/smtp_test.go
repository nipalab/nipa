package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeSMTPMessage struct {
	from string
	to   []string
	data string
}

type fakeSMTP struct {
	addr string
	mode string
	tls  *tls.Config

	mu       sync.Mutex
	messages []fakeSMTPMessage
	authSeen []string
}

func newFakeSMTP(t *testing.T, mode string) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &fakeSMTP{addr: listener.Addr().String(), mode: mode}
	if mode == smtpTLSSSL || mode == smtpTLSStartTLS {
		server.tls = testServerTLS(t)
	}
	go server.serve(listener)
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func testServerTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

func (s *fakeSMTP) serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(raw net.Conn) {
	defer raw.Close()
	conn := raw
	if s.mode == smtpTLSSSL {
		tlsConn := tls.Server(conn, s.tls)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
	}
	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = fmt.Fprintf(conn, "%s\r\n", line) }
	write("220 fake ESMTP")

	var msg fakeSMTPMessage
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-fake")
			if s.mode == smtpTLSStartTLS {
				write("250-STARTTLS")
			}
			write("250 AUTH PLAIN LOGIN")
		case upper == "STARTTLS":
			write("220 Ready to start TLS")
			tlsConn := tls.Server(conn, s.tls)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
		case strings.HasPrefix(upper, "AUTH "):
			s.mu.Lock()
			s.authSeen = append(s.authSeen, trimmed)
			s.mu.Unlock()
			write("235 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			msg = fakeSMTPMessage{from: strings.Trim(trimmed[len("MAIL FROM:"):], "<> ")}
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			msg.to = append(msg.to, strings.Trim(trimmed[len("RCPT TO:"):], "<> "))
			write("250 OK")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			data, err := readSMTPData(reader)
			if err != nil {
				return
			}
			msg.data = data
			s.mu.Lock()
			s.messages = append(s.messages, msg)
			s.mu.Unlock()
			write("250 OK")
		case upper == "QUIT":
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func readSMTPData(reader *bufio.Reader) (string, error) {
	var buf bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		if line == ".\r\n" {
			return buf.String(), nil
		}
		if strings.HasPrefix(line, ".") {
			line = line[1:]
		}
		buf.WriteString(line)
	}
}

func (s *fakeSMTP) delivered() []fakeSMTPMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fakeSMTPMessage, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *fakeSMTP) authAttempts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.authSeen))
	copy(out, s.authSeen)
	return out
}

func (s *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, portRaw, err := net.SplitHostPort(s.addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portRaw, "%d", &port)
	require.NoError(t, err)
	return host, port
}

func TestSMTPSender_Send(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSNone)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSNone}, "Nipa <noreply@example.com>", "reply@example.com", 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sender.Close() })

	err = sender.Send(context.Background(), Message{
		To:      []string{"Dev <dev@example.com>", "ops@example.com"},
		Subject: "Hello",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	})
	require.NoError(t, err)

	delivered := server.delivered()
	require.Len(t, delivered, 1)
	require.Equal(t, "noreply@example.com", delivered[0].from)
	require.Equal(t, []string{"dev@example.com", "ops@example.com"}, delivered[0].to)
	data := delivered[0].data
	require.Contains(t, data, "Subject: Hello")
	require.Contains(t, data, "MIME-Version: 1.0")
	require.Contains(t, data, "multipart/alternative")
	require.Contains(t, data, "Content-Type: text/plain; charset=utf-8")
	require.Contains(t, data, "Content-Type: text/html; charset=utf-8")
	require.Contains(t, data, "plain body")
	require.Contains(t, data, "<p>html body</p>")
	require.Contains(t, data, "Reply-To: reply@example.com")
	require.Contains(t, data, "\r\n")
}

func TestSMTPSender_TextOnly(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSNone)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSNone}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		To:      []string{"dev@example.com"},
		Subject: "Hi there",
		Text:    "h\u00e9llo",
	})
	require.NoError(t, err)

	delivered := server.delivered()
	require.Len(t, delivered, 1)
	require.Contains(t, delivered[0].data, "Content-Type: text/plain; charset=utf-8")
	require.NotContains(t, delivered[0].data, "multipart/alternative")
	require.Contains(t, delivered[0].data, "h=C3=A9llo")
}

func TestSMTPSender_Authentication(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSNone)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{
		Host:     host,
		Port:     port,
		TLS:      smtpTLSNone,
		Username: "mailer",
		Password: "secret",
	}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)
	require.NotEmpty(t, server.authAttempts())
	require.Contains(t, server.authAttempts()[0], "PLAIN")
}

func TestSMTPSender_StartTLS(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSStartTLS)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSStartTLS, InsecureSkipVerify: true}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "tls", Text: "b"})
	require.NoError(t, err)
	require.Len(t, server.delivered(), 1)
}

func TestSMTPSender_ImplicitTLS(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSSSL)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSSSL, InsecureSkipVerify: true}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "ssl", Text: "b"})
	require.NoError(t, err)
	require.Len(t, server.delivered(), 1)
}

func TestSMTPSender_RejectsHeaderInjection(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSNone)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSNone}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		To:        []string{"dev@example.com"},
		Subject:   "s",
		Text:      "b",
		MessageID: "x>\r\nBcc: attacker@example.com",
	})
	require.Error(t, err)
	require.Empty(t, server.delivered())
}

func TestSMTPSender_Overrides(t *testing.T) {
	server := newFakeSMTP(t, smtpTLSNone)
	host, port := server.hostPort(t)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSNone}, "default@example.com", "default-reply@example.com", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{
		From:    "Override <other@example.com>",
		ReplyTo: "reply@example.com",
		To:      []string{"dev@example.com"},
		Subject: "s",
		Text:    "b",
	})
	require.NoError(t, err)
	delivered := server.delivered()
	require.Len(t, delivered, 1)
	require.Equal(t, "other@example.com", delivered[0].from)
	require.Contains(t, delivered[0].data, "Reply-To: reply@example.com")
}

func TestSMTPSender_InvalidRecipient(t *testing.T) {
	sender, err := newSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 1, TLS: smtpTLSNone}, "noreply@example.com", "", 5*time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"bad address"}, Subject: "s", Text: "b"})
	require.Error(t, err)
}

func TestSMTPSender_ConnectionError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	host, portRaw, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portRaw, "%d", &port)
	require.NoError(t, err)

	sender, err := newSMTPSender(SMTPConfig{Host: host, Port: port, TLS: smtpTLSNone}, "noreply@example.com", "", time.Second)
	require.NoError(t, err)

	err = sender.Send(context.Background(), Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.Error(t, err)
}
