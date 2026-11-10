package registration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func termsServer(t *testing.T, status *atomic.Int32, body *atomic.Value) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTermsVersionSourceWithoutURLUsesTheFallback(t *testing.T) {
	source := NewTermsVersionSource(" ", " 2026-11-10 ", nil)
	require.Empty(t, source.URL())
	require.NoError(t, source.Refresh(context.Background()))
	require.Equal(t, "2026-11-10", source.Version())
}

func TestTermsVersionSourceFollowsThePublishedVersion(t *testing.T) {
	var status atomic.Int32
	var body atomic.Value
	status.Store(http.StatusOK)
	body.Store(`{"version":" 2026-11-10 "}`)
	server := termsServer(t, &status, &body)

	source := NewTermsVersionSource(server.URL, "2020-01-01", server.Client())
	require.Equal(t, "2020-01-01", source.Version())
	require.NoError(t, source.Refresh(context.Background()))
	require.Equal(t, "2026-11-10", source.Version())

	body.Store(`{"version":"2027-01-01"}`)
	require.NoError(t, source.Refresh(context.Background()))
	require.Equal(t, "2027-01-01", source.Version())
}

func TestTermsVersionSourceKeepsTheLastGoodVersion(t *testing.T) {
	var status atomic.Int32
	var body atomic.Value
	status.Store(http.StatusOK)
	body.Store(`{"version":"2026-11-10"}`)
	server := termsServer(t, &status, &body)

	source := NewTermsVersionSource(server.URL, "", server.Client())
	require.NoError(t, source.Refresh(context.Background()))

	status.Store(http.StatusBadGateway)
	require.ErrorContains(t, source.Refresh(context.Background()), "answered 502")

	status.Store(http.StatusOK)
	for _, bad := range []string{`not json`, `{"version":""}`, `{"version":"2026 11 10"}`, `{"version":"` + strings.Repeat("x", 65) + `"}`} {
		body.Store(bad)
		require.Error(t, source.Refresh(context.Background()), bad)
	}
	require.Equal(t, "2026-11-10", source.Version())

	server.Close()
	require.Error(t, source.Refresh(context.Background()))
	require.Equal(t, "2026-11-10", source.Version())

	broken := NewTermsVersionSource("http://bad host", "fallback", nil)
	require.Error(t, broken.Refresh(context.Background()))
	require.Equal(t, "fallback", broken.Version())
}

func TestTermsRefreshJobRefreshesUntilStopped(t *testing.T) {
	var status atomic.Int32
	var body atomic.Value
	status.Store(http.StatusOK)
	body.Store(`{"version":"2026-11-10"}`)
	server := termsServer(t, &status, &body)
	source := NewTermsVersionSource(server.URL, "2020-01-01", server.Client())

	job := NewTermsRefreshJob(source, 10*time.Millisecond)
	job.Start()
	require.Eventually(t, func() bool { return source.Version() == "2026-11-10" }, time.Second, 5*time.Millisecond)

	status.Store(http.StatusInternalServerError)
	time.Sleep(30 * time.Millisecond)
	require.Equal(t, "2026-11-10", source.Version())

	require.NoError(t, job.Stop(context.Background()))
}

func TestTermsRefreshJobStop(t *testing.T) {
	require.NoError(t, NewTermsRefreshJob(NewTermsVersionSource("", "", nil), 0).Stop(context.Background()))
	require.Equal(t, TermsRefreshInterval, NewTermsRefreshJob(nil, 0).interval)

	job := NewTermsRefreshJob(NewTermsVersionSource("", "", nil), time.Hour)
	job.Start()
	require.NoError(t, job.Stop(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stuck := &TermsRefreshJob{cancel: func() {}, done: make(chan struct{})}
	require.ErrorIs(t, stuck.Stop(ctx), context.Canceled)
}

func TestRegistrationFollowsTheLiveTermsVersion(t *testing.T) {
	env := newRegistrationEnv(t)
	version := "2026-11-10"
	env.registration.WithTermsVersion(func() string { return version })

	require.NoError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", true))
	version = "2027-01-01"
	require.NoError(t, env.registration.Register(context.Background(), "Bob", "bob@example.com", true))

	require.Equal(t, "2026-11-10", env.repo.terms[0].Version)
	require.Equal(t, "2027-01-01", env.repo.terms[1].Version)
	require.NoError(t, env.registration.AcceptTerms(context.Background(), 7, "2027-01-01"))
}
