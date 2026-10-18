// Package obs provides the observability seams shared by the free and
// enterprise server binaries: structured logging, request correlation, access
// logs, health probes and runtime diagnostics. Both editions wire it through
// internal/serverapp so their behavior is identical.
package obs

import (
	"context"
	"errors"
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
// a typo can never take the server down. A nil writer means stderr. Extra
// handlers (e.g. the OTLP log bridge) receive every record alongside the
// local writer; nil entries are ignored.
func Setup(level, format, service string, w io.Writer, extra ...slog.Handler) {
	if w == nil {
		w = os.Stderr
	}

	lvl, levelErr := ParseLevel(level)
	if levelErr != nil {
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var base slog.Handler
	var formatErr error
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "console", "text":
		base = slog.NewTextHandler(w, opts)
	case "json":
		base = slog.NewJSONHandler(w, opts)
	default:
		base = slog.NewTextHandler(w, opts)
		formatErr = fmt.Errorf("unknown log format %q", format)
	}

	handlers := []slog.Handler{base}
	for _, handler := range extra {
		if handler != nil {
			handlers = append(handlers, levelFilterHandler{level: lvl, next: handler})
		}
	}

	handler := base
	if len(handlers) > 1 {
		handler = fanoutHandler{handlers: handlers}
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

// fanoutHandler duplicates every record to all wrapped handlers, so logs keep
// flowing to stderr while an OTLP bridge ships them to a collector.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (h fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		next[i] = handler.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: next}
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		next[i] = handler.WithGroup(name)
	}
	return fanoutHandler{handlers: next}
}

// levelFilterHandler applies the process-wide LOG_LEVEL to handlers that have
// no level configuration of their own (the OTLP bridge).
type levelFilterHandler struct {
	level slog.Level
	next  slog.Handler
}

func (h levelFilterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level && h.next.Enabled(ctx, level)
}

func (h levelFilterHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.next.Handle(ctx, record)
}

func (h levelFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return levelFilterHandler{level: h.level, next: h.next.WithAttrs(attrs)}
}

func (h levelFilterHandler) WithGroup(name string) slog.Handler {
	return levelFilterHandler{level: h.level, next: h.next.WithGroup(name)}
}
