// Command migrate applies or reverts the Nipa Enterprise Edition postgres
// schema.
//
// Enterprise Edition: see ee/LICENSE.
package main

import (
	"flag"
	"log"

	eedb "github.com/nipalab/nipa/ee/db"
)

func main() {
	dsn := flag.String("dsn", "", "postgres connection string")
	action := flag.String("action", "up", "migration action: up or down")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("postgres DSN is required")
	}

	db, err := eedb.Open(*dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	switch *action {
	case "up":
		err = eedb.MigrateUp(db)
	case "down":
		err = eedb.MigrateDown(db)
	default:
		log.Fatalf("unknown action %q (expected up or down)", *action)
	}
	if err != nil {
		log.Fatalf("migrate %s: %v", *action, err)
	}
	log.Printf("migrate %s completed", *action)
}
