// Package obs provides the observability seams shared by the free and
// enterprise server binaries: structured logging, request correlation, access
// logs, health probes and runtime diagnostics. Both editions wire it through
// internal/serverapp so their behavior is identical.
package obs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const serviceAttr = "service"

// ParseLevel maps LOG_LEVEL values onto slog levels. An empty value means info.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", raw)
	}
}

// Setup installs the process-wide slog handler. Unknown levels and formats
// fall back to info/console and are reported through the installed logger, so
// a typo can never take the server down. A nil writer means stderr.
func Setup(level, format, service string, w io.Writer) {
	if w == nil {
		w = os.Stderr
	}

	lvl, levelErr := ParseLevel(level)
	if levelErr != nil {
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	var formatErr error
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "console", "text":
		handler = slog.NewTextHandler(w, opts)
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		handler = slog.NewTextHandler(w, opts)
		formatErr = fmt.Errorf("unknown log format %q", format)
	}

	logger := slog.New(handler)
	if service != "" {
		logger = logger.With(serviceAttr, service)
	}
	slog.SetDefault(logger)

	if levelErr != nil {
		slog.Warn("invalid LOG_LEVEL; using info", "value", level)
	}
	if formatErr != nil {
		slog.Warn("invalid LOG_FORMAT; using console", "value", format)
	}
}
