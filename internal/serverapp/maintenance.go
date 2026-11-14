package serverapp

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/usecase"
)

type maintenanceLister interface {
	List(ctx context.Context) ([]domain.MaintenanceWindow, error)
}

// MaintenanceLoader adapts the maintenance repository to the poller.
func MaintenanceLoader(repo maintenanceLister) maintenance.LoadFunc {
	return func(ctx context.Context) ([]maintenance.Window, error) {
		rows, err := repo.List(ctx)
		if err != nil {
			return nil, err
		}
		return usecase.WindowsFromDomain(rows), nil
	}
}

// NewMaintenance builds the maintenance service and its poller from
// MAINTENANCE_FILE and MAINTENANCE_POLL_SECONDS. Every published snapshot and
// failed poll is exported through metrics, which may be nil.
func NewMaintenance(cfg *config.Config, repo maintenanceLister, metrics *obs.Metrics) (*maintenance.Service, *maintenance.Poller) {
	svc := maintenance.NewService()
	svc.OnPublish(func(snap *maintenance.Snapshot, now time.Time) {
		publishMaintenanceMetrics(metrics, snap, now)
	})
	poller := maintenance.NewPoller(svc, MaintenanceLoader(repo), cfg.MaintenanceFilePath(), cfg.MaintenancePollInterval()).
		WithErrorHook(func(error) { metrics.ObserveMaintenancePollError() })
	return svc, poller
}

var (
	serverModes = []maintenance.Mode{maintenance.Off, maintenance.ReadOnly, maintenance.Full, maintenance.Frozen}
	windowModes = []maintenance.Mode{maintenance.ReadOnly, maintenance.Full}
)

func publishMaintenanceMetrics(m *obs.Metrics, snap *maintenance.Snapshot, now time.Time) {
	if m == nil {
		return
	}
	mode := maintenance.Off
	if snap.Server.Active(now) {
		mode = snap.Server.Mode
	}
	for _, md := range serverModes {
		value := 0.0
		if md == mode {
			value = 1
		}
		m.SetMaintenanceServerMode(string(md), value)
	}
	for scope, windows := range map[maintenance.Scope]map[string]*maintenance.Window{
		maintenance.ScopeOrg:     snap.Orgs,
		maintenance.ScopeProject: snap.Projects,
	} {
		counts := make(map[maintenance.Mode]int, len(windowModes))
		for _, w := range windows {
			if w.Active(now) {
				counts[w.Mode]++
			}
		}
		for _, md := range windowModes {
			m.SetMaintenanceWindows(string(scope), string(md), counts[md])
		}
	}
}
