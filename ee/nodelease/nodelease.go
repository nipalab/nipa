// Package nodelease hands every enterprise server instance a distinct
// snowflake node id by leasing it from the shared postgres database, so a
// multi-pod deployment never generates ids with a colliding node. When
// SNOWFLAKE_NODE_ID is set the instance uses that static id instead.
package nodelease

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/snow"
)

// nodeIDCount leases 0..254: snow.ReservedCLINode belongs to the maintenance
// command line.
const nodeIDCount = snow.ReservedCLINode

var ErrNoNodeID = errors.New("no snowflake node id available")

type Config struct {
	DB     *sql.DB
	Holder string
	TTL    time.Duration
	OnLost func(nodeID int64)
}

type Manager struct {
	queries *sqlcPostgres.Queries
	holder  string
	ttl     time.Duration
	onLost  func(nodeID int64)

	nodeID int64
	node   snow.Node

	cancel      context.CancelFunc
	done        chan struct{}
	renewMu     sync.Mutex
	lastRenewed time.Time

	pauseMu sync.Mutex
	paused  func() bool
}

func New(cfg Config) (*Manager, error) {
	if cfg.DB == nil {
		return nil, errors.New("node lease: database is required")
	}
	if cfg.Holder == "" {
		return nil, errors.New("node lease: holder is required")
	}
	if cfg.TTL < time.Second {
		return nil, fmt.Errorf("node lease: ttl must be at least one second, got %s", cfg.TTL)
	}

	onLost := cfg.OnLost
	if onLost == nil {
		onLost = func(nodeID int64) {
			slog.Error("snowflake node lease lost; shutting down to avoid id collisions", "node_id", nodeID)
			os.Exit(1)
		}
	}

	return &Manager{
		queries: sqlcPostgres.New(cfg.DB),
		holder:  cfg.Holder,
		ttl:     cfg.TTL,
		onLost:  onLost,
	}, nil
}

// Acquire claims a free or expired node id and starts renewing the lease.
// It returns ErrNoNodeID when every node id is held by a live instance.
func (m *Manager) Acquire(ctx context.Context) (snow.Node, error) {
	if m.node != nil {
		return m.node, nil
	}

	for candidate := int64(0); candidate < nodeIDCount; candidate++ {
		claimed, err := m.claim(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if !claimed {
			continue
		}

		node, err := snow.NewNode(candidate)
		if err != nil {
			return nil, err
		}

		m.nodeID = candidate
		m.node = node
		m.startHeartbeat()
		slog.Info("snowflake node lease acquired", "node_id", candidate, "holder", m.holder, "ttl", m.ttl)
		return node, nil
	}

	return nil, ErrNoNodeID
}

func (m *Manager) NodeID() int64 {
	return m.nodeID
}

// SetPause stops renewing while paused reports true (a frozen database): the
// heartbeat neither writes nor treats the ageing lease as lost. Resume must
// succeed before the pause ends.
func (m *Manager) SetPause(paused func() bool) {
	m.pauseMu.Lock()
	defer m.pauseMu.Unlock()
	m.paused = paused
}

func (m *Manager) isPaused() bool {
	m.pauseMu.Lock()
	paused := m.paused
	m.pauseMu.Unlock()
	return paused != nil && paused()
}

// Resume renews the lease after a pause, reclaiming the same node id when it
// expired meanwhile. Another instance holding the id calls OnLost, since this
// process may no longer mint ids with it; that and a database error are
// returned so the caller keeps the pause.
func (m *Manager) Resume(ctx context.Context) error {
	m.renewMu.Lock()
	defer m.renewMu.Unlock()
	if m.node == nil {
		return nil
	}
	renewed, err := m.renew(ctx)
	if err != nil {
		return fmt.Errorf("renew snowflake node lease: %w", err)
	}
	if !renewed {
		claimed, err := m.claim(ctx, m.nodeID)
		if err != nil {
			return err
		}
		if !claimed {
			slog.Error("snowflake node lease taken while paused", "node_id", m.nodeID, "holder", m.holder)
			m.onLost(m.nodeID)
			return fmt.Errorf("snowflake node %d is held by another instance", m.nodeID)
		}
	}
	m.lastRenewed = time.Now()
	slog.Info("snowflake node lease resumed", "node_id", m.nodeID)
	return nil
}

// Release stops the heartbeat and frees the node id. It is safe to call
// without a successful Acquire and more than once.
func (m *Manager) Release(ctx context.Context) {
	if m.cancel == nil {
		return
	}

	m.cancel()
	<-m.done
	m.cancel = nil
	m.node = nil

	if m.isPaused() {
		slog.Info("snowflake node lease kept while the database is frozen; it expires after its ttl", "node_id", m.nodeID)
		return
	}
	err := m.queries.NodeLeaseRelease(ctx, sqlcPostgres.NodeLeaseReleaseParams{
		NodeID: m.nodeID,
		Holder: m.holder,
	})
	if err != nil {
		slog.Warn("failed to release snowflake node lease", "node_id", m.nodeID, "error", err)
		return
	}
	slog.Info("snowflake node lease released", "node_id", m.nodeID)
}

func (m *Manager) claim(ctx context.Context, nodeID int64) (bool, error) {
	claimed, err := m.queries.NodeLeaseClaim(ctx, sqlcPostgres.NodeLeaseClaimParams{
		NodeID:     nodeID,
		Holder:     m.holder,
		TtlSeconds: m.ttlSeconds(),
	})
	if err != nil {
		return false, fmt.Errorf("claim snowflake node lease: %w", err)
	}
	return claimed == 1, nil
}

func (m *Manager) ttlSeconds() int64 {
	return int64(m.ttl / time.Second)
}

func (m *Manager) renewInterval() time.Duration {
	interval := m.ttl / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	return interval
}

func (m *Manager) startHeartbeat() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	m.renewMu.Lock()
	m.lastRenewed = time.Now()
	m.renewMu.Unlock()
	go m.heartbeat(ctx)
}

func (m *Manager) heartbeat(ctx context.Context) {
	defer close(m.done)

	ticker := time.NewTicker(m.renewInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.isPaused() {
				continue
			}
			if !m.keepAlive(ctx) {
				m.onLost(m.nodeID)
				return
			}
		}
	}
}

func (m *Manager) renew(ctx context.Context) (bool, error) {
	renewed, err := m.queries.NodeLeaseRenew(ctx, sqlcPostgres.NodeLeaseRenewParams{
		TtlSeconds: m.ttlSeconds(),
		NodeID:     m.nodeID,
		Holder:     m.holder,
	})
	return renewed == 1, err
}

func (m *Manager) keepAlive(ctx context.Context) bool {
	m.renewMu.Lock()
	defer m.renewMu.Unlock()
	renewed, err := m.renew(ctx)
	if err == nil && renewed {
		m.lastRenewed = time.Now()
		return true
	}

	if err == nil {
		claimed, claimErr := m.claim(ctx, m.nodeID)
		if claimErr == nil && claimed {
			m.lastRenewed = time.Now()
			return true
		}
		if claimErr != nil {
			err = claimErr
		} else {
			err = errors.New("lease is held by another instance")
		}
	}

	if time.Since(m.lastRenewed) >= m.ttl-m.renewInterval() {
		slog.Error("snowflake node lease could not be renewed before expiry", "node_id", m.nodeID, "holder", m.holder, "error", err)
		return false
	}

	slog.Warn("snowflake node lease renewal failed; retrying", "node_id", m.nodeID, "error", err)
	return true
}
