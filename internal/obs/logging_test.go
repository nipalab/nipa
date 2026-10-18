package obs

import (
	"bytes"
	"context"
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

// collector records the records and attributes it receives. It keeps state in
// place so assertions can look at the original value after WithAttrs.
type collector struct {
	records []slog.Record
	attrs   []slog.Attr
}

func (c *collector) Enabled(context.Context, slog.Level) bool { return true }

func (c *collector) Handle(_ context.Context, record slog.Record) error {
	c.records = append(c.records, record)
	return nil
}

func (c *collector) WithAttrs(attrs []slog.Attr) slog.Handler {
	c.attrs = append(c.attrs, attrs...)
	return c
}

func (c *collector) WithGroup(string) slog.Handler { return c }

func TestSetup_FansOutToExtraHandlers(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	extra := &collector{}
	Setup("info", "console", "nipad", &buf, extra)
	slog.Info("hello", "key", "value")
	slog.Debug("hidden")

	require.Contains(t, buf.String(), "hello")
	require.NotContains(t, buf.String(), "hidden")
	require.Len(t, extra.records, 1, "extra handlers must respect LOG_LEVEL")
	require.Equal(t, "hello", extra.records[0].Message)
	require.Equal(t, "value", recordAttr(extra.records[0], "key"))
}

func TestSetup_FanoutPropagatesAttrs(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	extra := &collector{}
	Setup("info", "console", "nipad", &buf, extra)
	slog.Info("hello")

	require.Len(t, extra.attrs, 1)
	require.Equal(t, serviceAttr, extra.attrs[0].Key)
	require.Equal(t, "nipad", extra.attrs[0].Value.String())
}

func TestSetup_NilExtraHandlerIgnored(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	Setup("info", "console", "", &buf, nil)
	require.NotPanics(t, func() { slog.Info("hello") })
	require.Contains(t, buf.String(), "hello")
}

func recordAttr(record slog.Record, key string) string {
	var value string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			value = attr.Value.String()
			return false
		}
		return true
	})
	return value
}
