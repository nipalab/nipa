package parity

import (
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
)

func methodNames(t reflect.Type) map[string]struct{} {
	out := make(map[string]struct{}, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out[t.Method(i).Name] = struct{}{}
	}
	return out
}

// Every query the free server runs must also exist for postgres, so a fix
// added to one backend cannot silently skip the other. Postgres may carry
// extra enterprise-only queries.
func TestQuerierParity_PostgresCoversSQLite(t *testing.T) {
	sqliteQueries := methodNames(reflect.TypeOf((*sqlcSqlite.Querier)(nil)).Elem())
	postgresQueries := methodNames(reflect.TypeOf((*sqlcPostgres.Querier)(nil)).Elem())

	var missing []string
	for name := range sqliteQueries {
		if _, ok := postgresQueries[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "sqlite queries without a postgres counterpart")
}
