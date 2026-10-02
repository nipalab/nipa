package db

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	sqlitemigrations "github.com/nipalab/nipa/db/migrations/sqlite"
)

func newMigrator(db *sql.DB, dialect string) (*migrate.Migrate, error) {
	var migrationsFS fs.FS

	switch dialect {
	case "sqlite3":
		migrationsFS = sqlitemigrations.FS
	default:
		return nil, fmt.Errorf("unsupported dialect %q", dialect)
	}

	dbDriver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return nil, err
	}

	sourceDriver, err := iofs.New(migrationsFS, ".")
	if err != nil {
		return nil, err
	}

	return migrate.NewWithInstance("iofs", sourceDriver, dialect, dbDriver)
}

func MigrateUp(db *sql.DB, dialect string) error {
	m, err := newMigrator(db, dialect)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func MigrateDown(db *sql.DB, dialect string) error {
	m, err := newMigrator(db, dialect)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
