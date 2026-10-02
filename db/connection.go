package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

var driverNames = map[string]string{
	"sqlite3": "sqlite",
}

func Open(dialect, dataSourceName string) (*sql.DB, error) {
	driverName, ok := driverNames[dialect]
	if !ok {
		return nil, fmt.Errorf("unsupported dialect %q", dialect)
	}

	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
