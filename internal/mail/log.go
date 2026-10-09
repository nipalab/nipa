package mail

import (
	"context"
	"log/slog"
	"strings"
)

// LogSender writes rendered messages to the server log. It exists for local
// development and tests: no email leaves the process.
type LogSender struct{}

func (s *LogSender) Send(ctx context.Context, msg Message) error {
	slog.InfoContext(ctx, "email delivery",
		"from", msg.From,
		"to", strings.Join(msg.To, ", "),
		"subject", msg.Subject,
		"text", msg.Text,
	)
	return nil
}

func (s *LogSender) Close() error { return nil }
