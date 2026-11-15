package serverapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/obs"
)

type listerFunc func(ctx context.Context) ([]domain.MaintenanceWindow, error)

func (f listerFunc) List(ctx context.Context) ([]domain.MaintenanceWindow, error) { return f(ctx) }

func TestNewMaintenancePublishesMetrics(t *testing.T) {
	now := time.Now()
	rows := []domain.MaintenanceWindow{
		{ID: 1, Mode: "full", StartsAt: now.Add(-time.Minute)},
		{ID: 2, OrgID: domain.IDPtr(5), OrgSlug: "acme", Mode: "read_only", StartsAt: now.Add(-time.Minute)},
		{ID: 3, ProjectID: domain.IDPtr(6), OrgSlug: "acme", ProjectSlug: "a", Mode: "full", StartsAt: now.Add(-time.Minute)},
		{ID: 4, ProjectID: domain.IDPtr(7), OrgSlug: "acme", ProjectSlug: "b", Mode: "full", StartsAt: now.Add(-time.Minute)},
		{ID: 5, ProjectID: domain.IDPtr(8), OrgSlug: "acme", ProjectSlug: "c", Mode: "read_only", StartsAt: now.Add(time.Hour)},
	}
	fail := false
	metrics := obs.NewMetrics()
	svc, poller := NewMaintenance(&config.Config{}, listerFunc(func(context.Context) ([]domain.MaintenanceWindow, error) {
		if fail {
			return nil, errors.New("db down")
		}
		return rows, nil
	}), metrics)
	require.NoError(t, poller.Poll(context.Background()))
	w, scope := svc.For("acme", "a")
	require.Equal(t, maintenance.Full, w.Mode)
	require.Equal(t, maintenance.ScopeServer, scope)

	body := scrape(t, metrics)
	require.Contains(t, body, `nipa_maintenance_server_mode{mode="full"} 1`)
	require.Contains(t, body, `nipa_maintenance_server_mode{mode="off"} 0`)
	require.Contains(t, body, `nipa_maintenance_windows{mode="full",scope="project"} 2`)
	require.Contains(t, body, `nipa_maintenance_windows{mode="read_only",scope="project"} 0`)
	require.Contains(t, body, `nipa_maintenance_windows{mode="read_only",scope="org"} 1`)

	fail = true
	poller.Start()
	require.Eventually(t, func() bool {
		return strings.Contains(scrape(t, metrics), "nipa_maintenance_poll_errors_total 1")
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, poller.Stop(context.Background()))
	require.Equal(t, maintenance.Full, svc.Snapshot().Server.Mode, "a failed load keeps the windows")
}

func TestNewMaintenanceReadsFileAndNilMetrics(t *testing.T) {
	file := filepath.Join(t.TempDir(), "maintenance")
	require.NoError(t, os.WriteFile(file, []byte(`{"mode":"read_only","message":"backup"}`), 0o600))
	svc, poller := NewMaintenance(&config.Config{MaintenanceFile: file, MaintenancePollSeconds: 1}, listerFunc(func(context.Context) ([]domain.MaintenanceWindow, error) {
		return nil, nil
	}), nil)
	require.NoError(t, poller.Poll(context.Background()))
	require.Equal(t, maintenance.SourceFile, svc.Snapshot().Source)
	w, _ := svc.For("", "")
	require.Equal(t, maintenance.ReadOnly, w.Mode)
}

func TestLoadMaintenanceLogsFailures(t *testing.T) {
	svc, poller := NewMaintenance(&config.Config{}, listerFunc(func(context.Context) ([]domain.MaintenanceWindow, error) {
		return nil, errors.New("db down")
	}), nil)
	loadMaintenance(svc, poller)
	loadMaintenance(nil, poller)
	require.Equal(t, maintenance.SourceDB, svc.Snapshot().Source)
}

func scrape(t *testing.T, m *obs.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Body.String()
}

type pausableDispatcher struct {
	paused func() bool
}

func (d *pausableDispatcher) Start()                     {}
func (d *pausableDispatcher) Stop(context.Context) error { return nil }
func (d *pausableDispatcher) SetPause(p func() bool)     { d.paused = p }

type plainDispatcher struct{}

func (plainDispatcher) Start()                     {}
func (plainDispatcher) Stop(context.Context) error { return nil }

func TestPauseWhileFrozen(t *testing.T) {
	d := &pausableDispatcher{}
	PauseWhileFrozen(nil, d)
	require.Nil(t, d.paused, "no service, no pause")

	svc := maintenance.NewService()
	PauseWhileFrozen(svc, d, plainDispatcher{}, nil)
	require.NotNil(t, d.paused)
	require.False(t, d.paused())
	require.NoError(t, svc.Apply(context.Background(), maintenance.NewSnapshot([]maintenance.Window{{ID: 1, Mode: maintenance.Frozen, StartsAt: time.Now().Add(-time.Second)}}, time.Now())))
	require.True(t, d.paused())
	require.NoError(t, svc.Apply(context.Background(), maintenance.NewSnapshot([]maintenance.Window{{ID: 1, Mode: maintenance.Full, StartsAt: time.Now().Add(-time.Second)}}, time.Now())))
	require.False(t, d.paused(), "only frozen pauses")
}
