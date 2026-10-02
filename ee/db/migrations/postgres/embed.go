// Package migrations embeds the postgres SQL migration files for use by the
// Enterprise Edition migration runner.
//
// Enterprise Edition: see ee/LICENSE.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
