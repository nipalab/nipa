package mail

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogSender_Send(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	sender := &LogSender{}
	err := sender.Send(context.Background(), Message{
		From:    "noreply@example.com",
		To:      []string{"dev@example.com", "ops@example.com"},
		Subject: "MR ready",
		Text:    "please review",
	})
	require.NoError(t, err)

	logged := buf.String()
	require.Contains(t, logged, "email delivery")
	require.Contains(t, logged, "subject=\"MR ready\"")
	require.Contains(t, logged, "dev@example.com, ops@example.com")
	require.Contains(t, logged, "please review")
	require.NoError(t, sender.Close())
}
