//go:build companion

package companion

import "embed"

// all: keeps the package building (bin/.gitignore) before the CLI is built.
//
//go:embed all:bin
var files embed.FS

var payload, _ = files.ReadFile("bin/nipa")
