// Package db opens and migrates the postgres database of the Nipa Enterprise
// Edition server.
//
// Enterprise Edition: see ee/LICENSE.
package db

import (
	"database/sql"
	"errors"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	postgresmigrations "github.com/nipalab/nipa/ee/db/migrations/postgres"
	"github.com/nipalab/nipa/internal/config"
)

// DefaultMaxOpenConns is the pool size used when PoolConfig.MaxOpenConns is
// not positive. database/sql would otherwise leave the pool unbounded.
const DefaultMaxOpenConns = config.DefaultDatabaseMaxOpenConns

// adminMaxOpenConns sizes the pool of one-shot commands.
const adminMaxOpenConns = 4

// PoolConfig bounds the connection pool Open creates. The zero value is a
// safe pool of DefaultMaxOpenConns connections.
type PoolConfig struct {
	// MaxOpenConns caps open connections; non-positive means
	// DefaultMaxOpenConns.
	MaxOpenConns int
	// MaxIdleConns caps idle connections; non-positive means MaxOpenConns,
	// so a busy server reuses its connections instead of reopening them.
	MaxIdleConns int
	// ConnMaxLifetime and ConnMaxIdleTime recycle connections; non-positive
	// means no limit.
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// AdminPoolConfig is the small fixed pool for one-shot administrative
// commands (migrate CLI, `nipad-ee maintenance`), which need no tuning.
func AdminPoolConfig() PoolConfig {
	return PoolConfig{MaxOpenConns: adminMaxOpenConns}
}

// normalized resolves the defaulting rules of PoolConfig.
func (p PoolConfig) normalized() PoolConfig {
	if p.MaxOpenConns <= 0 {
		p.MaxOpenConns = DefaultMaxOpenConns
	}
	if p.MaxIdleConns <= 0 || p.MaxIdleConns > p.MaxOpenConns {
		p.MaxIdleConns = p.MaxOpenConns
	}
	if p.ConnMaxLifetime < 0 {
		p.ConnMaxLifetime = 0
	}
	if p.ConnMaxIdleTime < 0 {
		p.ConnMaxIdleTime = 0
	}
	return p
}

func (p PoolConfig) apply(db *sql.DB) {
	p = p.normalized()
	db.SetMaxOpenConns(p.MaxOpenConns)
	db.SetMaxIdleConns(p.MaxIdleConns)
	db.SetConnMaxLifetime(p.ConnMaxLifetime)
	db.SetConnMaxIdleTime(p.ConnMaxIdleTime)
}

// Open connects to the postgres DSN with a bounded connection pool and
// verifies the connection.
func Open(dsn string, pool PoolConfig) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	pool.apply(db)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func newMigrator(db *sql.DB) (*migrate.Migrate, error) {
	dbDriver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return nil, err
	}
	sourceDriver, err := iofs.New(postgresmigrations.FS, ".")
	if err != nil {
		return nil, err
	}
	return migrate.NewWithInstance("iofs", sourceDriver, "postgres", dbDriver)
}

// MigrateUp applies every pending postgres migration.
func MigrateUp(db *sql.DB) error {
	m, err := newMigrator(db)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// MigrateDown reverts every applied postgres migration.
func MigrateDown(db *sql.DB) error {
	m, err := newMigrator(db)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
