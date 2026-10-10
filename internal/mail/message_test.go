package mail

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseAddress(t *testing.T) {
	addr, err := parseAddress("Dev <dev@example.com>", "to")
	require.NoError(t, err)
	require.Equal(t, "Dev", addr.Name)
	require.Equal(t, "dev@example.com", addr.Addr)

	addr, err = parseAddress("dev@example.com", "to")
	require.NoError(t, err)
	require.Empty(t, addr.Name)
	require.Equal(t, "dev@example.com", addr.Addr)

	_, err = parseAddress("", "to")
	require.Error(t, err)

	_, err = parseAddress("not an address", "to")
	require.Error(t, err)
}

func TestValidAddress(t *testing.T) {
	require.True(t, ValidAddress("dev@example.com"))
	require.True(t, ValidAddress("Dev <dev@example.com>"))
	require.True(t, ValidAddress(" dev@example.com "))
	require.False(t, ValidAddress(""))
	require.False(t, ValidAddress("nipa"))
	require.False(t, ValidAddress("a b@example.com"))
}

func TestParseAddresses(t *testing.T) {
	addrs, err := parseAddresses([]string{"a@example.com", "B <b@example.com>"}, "to")
	require.NoError(t, err)
	require.Len(t, addrs, 2)
	require.Equal(t, "B", addrs[1].Name)

	_, err = parseAddresses(nil, "to")
	require.Error(t, err)

	_, err = parseAddresses([]string{"a@example.com", "bad"}, "to")
	require.Error(t, err)
}

func TestMIMEBytes_TextOnly(t *testing.T) {
	from, err := parseAddress("Nipa <noreply@example.com>", "from")
	require.NoError(t, err)
	to, err := parseAddresses([]string{"dev@example.com"}, "to")
	require.NoError(t, err)

	raw, err := mimeBytes(Message{Subject: "Hello", Text: "body text"}, from, to, nil, time.Unix(0, 0).UTC())
	require.NoError(t, err)
	message := string(raw)

	require.Contains(t, message, "From: Nipa <noreply@example.com>")
	require.Contains(t, message, "To: dev@example.com")
	require.Contains(t, message, "Subject: Hello")
	require.Contains(t, message, "Content-Type: text/plain; charset=utf-8")
	require.Contains(t, message, "Content-Transfer-Encoding: quoted-printable")
	require.NotContains(t, message, "multipart/alternative")
	require.True(t, strings.HasSuffix(message, "body text"))
	require.NotContains(t, message, "\r\n")
}

func TestMIMEBytes_Multipart(t *testing.T) {
	from, err := parseAddress("noreply@example.com", "from")
	require.NoError(t, err)
	to, err := parseAddresses([]string{"dev@example.com"}, "to")
	require.NoError(t, err)
	replyTo, err := parseAddress("support@example.com", "reply-to")
	require.NoError(t, err)

	raw, err := mimeBytes(Message{
		Subject:    "H\u00e9llo",
		Text:       "plain",
		HTML:       "<p>html</p>",
		MessageID:  "msg-1@nipa",
		InReplyTo:  "root@nipa",
		References: []string{"root@nipa", "<prev@nipa>"},
	}, from, to, &replyTo, time.Unix(0, 0).UTC())
	require.NoError(t, err)
	message := string(raw)

	require.Contains(t, message, "Reply-To: support@example.com")
	require.Contains(t, message, "Message-ID: <msg-1@nipa>")
	require.Contains(t, message, "In-Reply-To: <root@nipa>")
	require.Contains(t, message, "References: <root@nipa> <prev@nipa>")
	require.Contains(t, message, "Subject: =?utf-8?q?H=C3=A9llo?=")
	require.Contains(t, message, "multipart/alternative; boundary=nipa-")
	require.Contains(t, message, "Content-Type: text/plain; charset=utf-8")
	require.Contains(t, message, "Content-Type: text/html; charset=utf-8")
	require.Contains(t, message, "<p>html</p>")
}

func TestMIMEBytes_QuotedPrintableWrapping(t *testing.T) {
	from, err := parseAddress("noreply@example.com", "from")
	require.NoError(t, err)
	to, err := parseAddresses([]string{"dev@example.com"}, "to")
	require.NoError(t, err)

	long := strings.Repeat("a", 200)
	raw, err := mimeBytes(Message{Subject: "s", Text: long}, from, to, nil, time.Unix(0, 0).UTC())
	require.NoError(t, err)

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		require.LessOrEqual(t, len(line), 76, "quoted-printable line exceeds 76 chars")
	}
}

func TestFormatAddress(t *testing.T) {
	require.Equal(t, "dev@example.com", formatAddress(address{Addr: "dev@example.com"}))
	require.Equal(t, "Dev <dev@example.com>", formatAddress(address{Name: "Dev", Addr: "dev@example.com"}))
	require.Equal(t, "=?utf-8?q?D=C3=A9v?= <dev@example.com>", formatAddress(address{Name: "D\u00e9v", Addr: "dev@example.com"}))
}

func TestResolveAddressOverrides(t *testing.T) {
	fallback, err := parseAddress("default@example.com", "from")
	require.NoError(t, err)

	from, err := resolveFrom(Message{}, fallback)
	require.NoError(t, err)
	require.Equal(t, "default@example.com", from.Addr)

	from, err = resolveFrom(Message{From: "Override <other@example.com>"}, fallback)
	require.NoError(t, err)
	require.Equal(t, "other@example.com", from.Addr)

	_, err = resolveFrom(Message{From: "bad"}, fallback)
	require.Error(t, err)

	replyTo, err := resolveReplyTo(Message{}, nil)
	require.NoError(t, err)
	require.Nil(t, replyTo)

	replyTo, err = resolveReplyTo(Message{ReplyTo: "reply@example.com"}, nil)
	require.NoError(t, err)
	require.Equal(t, "reply@example.com", replyTo.Addr)

	_, err = resolveReplyTo(Message{ReplyTo: "bad"}, nil)
	require.Error(t, err)
}

func TestNormalizeMessageID(t *testing.T) {
	for _, raw := range []string{"a@b", "<a@b>", " <a@b> "} {
		id, err := normalizeMessageID(raw)
		require.NoError(t, err)
		require.Equal(t, "<a@b>", id)
	}

	for _, raw := range []string{"", "  ", "a@b\r\nBcc: attacker@example.com", "a\nb", "a\rb", "a\tb", "a\x7fb"} {
		_, err := normalizeMessageID(raw)
		require.Error(t, err, "message id %q should be rejected", raw)
	}
}
