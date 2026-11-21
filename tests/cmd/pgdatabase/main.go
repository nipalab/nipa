// Command pgdatabase recreates an empty postgres database next to the one a
// DSN names and prints the DSN of the new database; used by tests/run.sh to
// give each extra nipad-ee instance (rate-limit, MFA-off, invite-only
// profiles) its own database on the shared postgres server.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var namePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func main() {
	dsn := flag.String("dsn", "", "postgres URL DSN of an existing database")
	name := flag.String("name", "", "database to recreate")
	flag.Parse()

	if *dsn == "" || !namePattern.MatchString(*name) {
		log.Fatal("pgdatabase: -dsn and a lowercase -name are required")
	}
	target, err := url.Parse(*dsn)
	if err != nil || target.Scheme == "" {
		log.Fatalf("pgdatabase: -dsn must be a postgres:// URL: %v", err)
	}

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatalf("pgdatabase: open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The name is validated above, so quoting it is enough.
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, *name)); err != nil {
		log.Fatalf("pgdatabase: drop %s: %v", *name, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, *name)); err != nil {
		log.Fatalf("pgdatabase: create %s: %v", *name, err)
	}

	target.Path = "/" + *name
	fmt.Println(target.String())
}
