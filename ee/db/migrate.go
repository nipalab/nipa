// Package db opens and migrates the postgres database of the Nipa Enterprise
// Edition server.
//
// Enterprise Edition: see ee/LICENSE.
package db

import (
	"database/sql"
	"errors"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	postgresmigrations "github.com/nipalab/nipa/ee/db/migrations/postgres"
)

// Open connects to the postgres DSN and verifies the connection.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
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
