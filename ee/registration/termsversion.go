package registration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	termsDocumentMaxBytes = 64 * 1024
	termsVersionMaxLen    = 64
	TermsRefreshInterval  = 15 * time.Minute
	termsRefreshTimeout   = 10 * time.Second
)

// TermsVersionSource serves the published terms version. It starts from the
// configured fallback and, when a URL is set, follows the version the website
// publishes; a failed fetch keeps the last good value.
type TermsVersionSource struct {
	url      string
	fallback string
	client   *http.Client
	current  atomic.Pointer[string]
}

func NewTermsVersionSource(url, fallback string, client *http.Client) *TermsVersionSource {
	if client == nil {
		client = &http.Client{Timeout: termsRefreshTimeout}
	}
	return &TermsVersionSource{
		url:      strings.TrimSpace(url),
		fallback: strings.TrimSpace(fallback),
		client:   client,
	}
}

func (s *TermsVersionSource) URL() string {
	return s.url
}

func (s *TermsVersionSource) Version() string {
	if v := s.current.Load(); v != nil {
		return *v
	}
	return s.fallback
}

// Refresh fetches the published version once.
func (s *TermsVersionSource) Refresh(ctx context.Context) error {
	if s.url == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("terms version: %s answered %d", s.url, resp.StatusCode)
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, termsDocumentMaxBytes)).Decode(&doc); err != nil {
		return fmt.Errorf("terms version: decode %s: %w", s.url, err)
	}
	version := strings.TrimSpace(doc.Version)
	if !validTermsVersion(version) {
		return fmt.Errorf("terms version: %s published an invalid version %q", s.url, version)
	}
	if previous := s.Version(); previous != version {
		slog.Info("terms version updated", "previous", previous, "version", version)
	}
	s.current.Store(&version)
	return nil
}

func validTermsVersion(version string) bool {
	if version == "" || len(version) > termsVersionMaxLen {
		return false
	}
	for _, r := range version {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// TermsRefreshJob refreshes the source in the background until shutdown.
type TermsRefreshJob struct {
	source   *TermsVersionSource
	interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewTermsRefreshJob(source *TermsVersionSource, interval time.Duration) *TermsRefreshJob {
	if interval <= 0 {
		interval = TermsRefreshInterval
	}
	return &TermsRefreshJob{source: source, interval: interval}
}

func (j *TermsRefreshJob) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.done = make(chan struct{})
	go func() {
		defer close(j.done)
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				j.refresh(ctx)
			}
		}
	}()
}

func (j *TermsRefreshJob) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, termsRefreshTimeout)
	defer cancel()
	if err := j.source.Refresh(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("terms version refresh failed; keeping the current version", "version", j.source.Version(), "err", err)
	}
}

func (j *TermsRefreshJob) Stop(ctx context.Context) error {
	if j.cancel == nil {
		return nil
	}
	j.cancel()
	select {
	case <-j.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
