package obs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"", slog.LevelInfo, false},
		{"info", slog.LevelInfo, false},
		{"INFO", slog.LevelInfo, false},
		{" debug ", slog.LevelDebug, false},
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"hush", slog.LevelInfo, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSetup_JSON(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	Setup("debug", "json", "nipad", &buf)
	slog.Debug("hello", "key", "value")

	var record map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record))
	require.Equal(t, "hello", record["msg"])
	require.Equal(t, "DEBUG", record["level"])
	require.Equal(t, "nipad", record["service"])
	require.Equal(t, "value", record["key"])
}

func TestSetup_ConsoleAndLevel(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	Setup("warn", "console", "nipad-ee", &buf)
	slog.Info("hidden")
	slog.Warn("visible")

	out := buf.String()
	require.NotContains(t, out, "hidden")
	require.Contains(t, out, "visible")
	require.Contains(t, out, "service=nipad-ee")
}

func TestSetup_Fallbacks(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	Setup("hush", "yaml", "", &buf)
	slog.Info("visible")
	slog.Debug("hidden")

	out := buf.String()
	require.Contains(t, out, "visible")
	require.NotContains(t, out, "hidden")
	require.Contains(t, out, "invalid LOG_LEVEL")
	require.Contains(t, out, "invalid LOG_FORMAT")
}

func TestSetup_NilWriterDefaultsToStderr(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	Setup("info", "console", "", nil)
	require.NotPanics(t, func() { slog.Info("to stderr") })
}
