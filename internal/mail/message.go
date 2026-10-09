package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// Message is one outbound email. From, ReplyTo and To accept either a bare
// address or the "Display Name <addr>" form. MessageID, InReplyTo and
// References carry RFC 5322 threading headers; an empty MessageID lets the
// provider assign one.
type Message struct {
	From       string
	ReplyTo    string
	To         []string
	Subject    string
	Text       string
	HTML       string
	MessageID  string
	InReplyTo  string
	References []string
}

// Sender delivers one message per Send call. Implementations are safe for
// concurrent use and never retry: retries are the caller's job.
type Sender interface {
	Send(ctx context.Context, msg Message) error
	Close() error
}

type address struct {
	Name string
	Addr string
}

func parseAddress(raw, field string) (address, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return address{}, fmt.Errorf("empty %s address", field)
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil {
		return address{}, fmt.Errorf("invalid %s address %q: %w", field, raw, err)
	}
	return address{Name: parsed.Name, Addr: parsed.Address}, nil
}

func parseAddresses(raw []string, field string) ([]address, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("no %s recipients", field)
	}
	out := make([]address, 0, len(raw))
	for _, entry := range raw {
		parsed, err := parseAddress(entry, field)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

// resolveFrom returns the per-message From when set, otherwise the sender
// default.
func resolveFrom(msg Message, fallback address) (address, error) {
	if strings.TrimSpace(msg.From) == "" {
		return fallback, nil
	}
	return parseAddress(msg.From, "from")
}

func resolveReplyTo(msg Message, fallback *address) (*address, error) {
	if strings.TrimSpace(msg.ReplyTo) == "" {
		return fallback, nil
	}
	parsed, err := parseAddress(msg.ReplyTo, "reply-to")
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func formatAddress(addr address) string {
	if addr.Name == "" {
		return addr.Addr
	}
	return fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", addr.Name), addr.Addr)
}

// mimeBytes renders the message as an RFC 5322 MIME document with LF line
// endings; the SMTP client's dot writer converts them to CRLF.
func mimeBytes(msg Message, from address, to []address, replyTo *address, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	writeHeader := func(name, value string) {
		fmt.Fprintf(&buf, "%s: %s\n", name, value)
	}

	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("From", formatAddress(from))
	toParts := make([]string, 0, len(to))
	for _, addr := range to {
		toParts = append(toParts, formatAddress(addr))
	}
	writeHeader("To", strings.Join(toParts, ", "))
	if replyTo != nil {
		writeHeader("Reply-To", formatAddress(*replyTo))
	}
	if msg.MessageID != "" {
		id, err := normalizeMessageID(msg.MessageID)
		if err != nil {
			return nil, err
		}
		writeHeader("Message-ID", id)
	}
	if msg.InReplyTo != "" {
		id, err := normalizeMessageID(msg.InReplyTo)
		if err != nil {
			return nil, err
		}
		writeHeader("In-Reply-To", id)
	}
	if len(msg.References) > 0 {
		refs := make([]string, 0, len(msg.References))
		for _, ref := range msg.References {
			id, err := normalizeMessageID(ref)
			if err != nil {
				return nil, err
			}
			refs = append(refs, id)
		}
		writeHeader("References", strings.Join(refs, " "))
	}
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHeader("MIME-Version", "1.0")

	if msg.HTML == "" {
		writeHeader("Content-Type", "text/plain; charset=utf-8")
		writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\n")
		if err := writeQuotedPrintable(&buf, msg.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	writeHeader("Content-Type", "multipart/alternative; boundary="+boundary)
	buf.WriteString("\n")
	if err := writePart(&buf, boundary, "text/plain; charset=utf-8", msg.Text); err != nil {
		return nil, err
	}
	if err := writePart(&buf, boundary, "text/html; charset=utf-8", msg.HTML); err != nil {
		return nil, err
	}
	fmt.Fprintf(&buf, "--%s--\n", boundary)
	return buf.Bytes(), nil
}

func writePart(buf *bytes.Buffer, boundary, contentType, body string) error {
	fmt.Fprintf(buf, "--%s\n", boundary)
	fmt.Fprintf(buf, "Content-Type: %s\n", contentType)
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\n\n")
	if err := writeQuotedPrintable(buf, body); err != nil {
		return err
	}
	buf.WriteString("\n")
	return nil
}

func writeQuotedPrintable(buf *bytes.Buffer, body string) error {
	writer := quotedprintable.NewWriter(buf)
	if _, err := writer.Write([]byte(strings.ReplaceAll(body, "\r\n", "\n"))); err != nil {
		return err
	}
	return writer.Close()
}

func randomBoundary() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate mime boundary: %w", err)
	}
	return "nipa-" + hex.EncodeToString(raw[:]), nil
}

func normalizeMessageID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", fmt.Errorf("empty message id")
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid message id %q", id)
		}
	}
	if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") {
		return trimmed, nil
	}
	return "<" + trimmed + ">", nil
}
