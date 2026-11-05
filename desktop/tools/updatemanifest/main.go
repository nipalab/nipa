// Command updatemanifest signs desktop release artifacts into update.json.
package main

import (
	"fmt"
	"os"

	"github.com/nipalab/nipa/desktop/internal/update"
)

func main() {
	if err := update.ManifestCommand(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "updatemanifest:", err)
		os.Exit(1)
	}
}
