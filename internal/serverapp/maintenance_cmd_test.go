package serverapp

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/usecase"
)

func sqliteMaintenanceOpener(cfg *config.Config) (usecase.Repositories, *sql.DB, error) {
	conn, err := db.OpenSQLite(cfg.DatabaseDSN)
	if err != nil {
		return usecase.Repositories{}, nil, err
	}
	return usecase.Repositories{
		Org:         sqlite.NewOrgRepository(conn),
		Project:     sqlite.NewProjectRepository(conn),
		PBAC:        sqlite.NewPBACRepository(conn),
		Audit:       sqlite.NewAuditRepository(conn),
		Maintenance: sqlite.NewMaintenanceRepository(conn),
		Email:       sqlite.NewEmailRepository(conn),
	}, conn, nil
}

func migratedDSN(t *testing.T) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "nipa.db")
	conn, err := db.OpenSQLite(dsn)
	require.NoError(t, err)
	require.NoError(t, db.MigrateUp(conn, "sqlite3"))
	require.NoError(t, conn.Close())
	return dsn
}

func runCmd(t *testing.T, cfg *config.Config, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runMaintenanceCommand(context.Background(), cfg, args, sqliteMaintenanceOpener, &out)
	return out.String(), err
}

func TestRunMaintenanceCommandNotHandled(t *testing.T) {
	for _, args := range [][]string{nil, {"-version"}, {"serve"}} {
		handled, err := RunMaintenanceCommand(args, sqliteMaintenanceOpener, &bytes.Buffer{})
		require.False(t, handled)
		require.NoError(t, err)
	}
}

func TestRunMaintenanceCommandLoadsConfig(t *testing.T) {
	dsn := migratedDSN(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("DATABASE_DSN: "+dsn+"\n"), 0o600))
	t.Chdir(dir)
	t.Setenv("DATABASE_DSN", dsn)
	t.Setenv("MAINTENANCE_FILE", "")

	var out bytes.Buffer
	handled, err := RunMaintenanceCommand([]string{"maintenance", "status"}, sqliteMaintenanceOpener, &out)
	require.True(t, handled)
	require.NoError(t, err)
	require.Equal(t, "server off (source db)\n", out.String())

	require.NoError(t, os.Remove(filepath.Join(dir, "config.yaml")))
	handled, err = RunMaintenanceCommand([]string{"maintenance", "status"}, sqliteMaintenanceOpener, &out)
	require.True(t, handled)
	require.Error(t, err)
}

func TestMaintenanceCommandSetStatusEnd(t *testing.T) {
	cfg := &config.Config{DatabaseDSN: migratedDSN(t)}

	out, err := runCmd(t, cfg, "set", "read_only", "-m", "disk swap", "--end", "2999-01-01T00:00:00Z", "--notify")
	require.NoError(t, err)
	require.Contains(t, out, "maintenance set: server read_only (active")
	require.Contains(t, out, "until about 2999-01-01T00:00:00Z")
	require.Contains(t, out, ": disk swap")
	require.Contains(t, out, "notified: false")

	start := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	out, err = runCmd(t, cfg, "set", "--org", "default", "full", "--start", start)
	require.NoError(t, err)
	require.Contains(t, out, "organization default full (scheduled, from "+start+")")

	out, err = runCmd(t, cfg, "set", "full", "--project", "default/default")
	require.NoError(t, err)
	require.Contains(t, out, "project default/default full (active")

	out, err = runCmd(t, cfg, "status")
	require.NoError(t, err)
	require.Contains(t, out, "server read_only (active")
	require.Contains(t, out, "[source db]")
	require.Contains(t, out, "organization default full (scheduled")
	require.Contains(t, out, "project default/default full (active")

	_, err = runCmd(t, cfg, "set", "frozen", "--org", "default")
	require.ErrorContains(t, err, "frozen is only available for the server window")
	out, err = runCmd(t, cfg, "set", "frozen", "-m", "moving the database")
	require.NoError(t, err)
	require.Contains(t, out, "maintenance set: server frozen (active")
	_, err = runCmd(t, cfg, "set", "full", "--org", "missing")
	require.ErrorContains(t, err, "not found")

	for _, args := range [][]string{{"end", "--project", "default/default"}, {"end", "--org", "default"}, {"end"}} {
		out, err = runCmd(t, cfg, args...)
		require.NoError(t, err, args)
		require.Contains(t, out, "maintenance ended")
	}
	_, err = runCmd(t, cfg, "end")
	require.ErrorContains(t, err, "no maintenance window")

	out, err = runCmd(t, cfg, "status")
	require.NoError(t, err)
	require.Equal(t, "server off (source db)\n", out)

	conn, err := db.OpenSQLite(cfg.DatabaseDSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	entries, err := sqlite.NewAuditRepository(conn).List(context.Background(), domain.AuditFilter{ActionPrefix: "maintenance", Limit: 50})
	require.NoError(t, err)
	require.Len(t, entries, 7)
	for _, entry := range entries {
		require.Equal(t, domain.AuditActorSystem, entry.ActorType)
		require.Nil(t, entry.ActorUserID)
		require.Equal(t, "cli", entry.Metadata["via"])
	}
	rows, err := sqlite.NewMaintenanceRepository(conn).List(context.Background())
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestMaintenanceCommandFileOverride(t *testing.T) {
	file := filepath.Join(t.TempDir(), "maintenance")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	cfg := &config.Config{DatabaseDSN: migratedDSN(t), MaintenanceFile: file}

	_, err := runCmd(t, cfg, "set", "read_only")
	require.ErrorContains(t, err, "MAINTENANCE_FILE")
	out, err := runCmd(t, cfg, "status")
	require.NoError(t, err)
	require.Contains(t, out, "server full (active")
	require.Contains(t, out, "[source file]")

	require.NoError(t, os.WriteFile(file, []byte("{bad"), 0o600))
	out, err = runCmd(t, cfg, "status")
	require.NoError(t, err)
	require.Contains(t, out, "warning: parse maintenance file")
}

func TestMaintenanceCommandMissingTable(t *testing.T) {
	cfg := &config.Config{DatabaseDSN: filepath.Join(t.TempDir(), "empty.db")}
	_, err := runCmd(t, cfg, "status")
	require.ErrorIs(t, err, errMaintenanceSchema)
	require.ErrorContains(t, err, "schema too old, start the server once")
}

func TestMaintenanceCommandOpenErrors(t *testing.T) {
	cfg := &config.Config{DatabaseDSN: migratedDSN(t)}
	var out bytes.Buffer
	err := runMaintenanceCommand(context.Background(), cfg, []string{"status"}, func(*config.Config) (usecase.Repositories, *sql.DB, error) {
		return usecase.Repositories{}, nil, errors.New("no database")
	}, &out)
	require.EqualError(t, err, "no database")

	conn, err := db.OpenSQLite(cfg.DatabaseDSN)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	err = checkMaintenanceSchema(context.Background(), conn)
	require.ErrorContains(t, err, "read maintenance windows")
}

func TestMaintenanceCommandBadArguments(t *testing.T) {
	cfg := &config.Config{DatabaseDSN: migratedDSN(t)}
	cases := []struct {
		args []string
		msg  string
	}{
		{nil, "usage:"},
		{[]string{"start"}, `unknown maintenance command "start"`},
		{[]string{"set"}, "a mode is required"},
		{[]string{"set", "--org", "a"}, "a mode is required"},
		{[]string{"set", "full", "extra"}, `unexpected argument "extra"`},
		{[]string{"set", "--bogus", "full"}, "flag provided but not defined"},
		{[]string{"set", "--org", "a", "full", "--bogus"}, "flag provided but not defined"},
		{[]string{"set", "full", "--org", "a", "--project", "a/b"}, "either --org or --project"},
		{[]string{"set", "full", "--project", "nope"}, "--project must be <org>/<project>"},
		{[]string{"set", "full", "--project", "a/b/c"}, "--project must be <org>/<project>"},
		{[]string{"set", "full", "--start", "tomorrow"}, "--start must be RFC3339"},
		{[]string{"set", "full", "--end", "soon"}, "--end must be RFC3339"},
		{[]string{"set", "closed"}, "mode must be read_only, full or frozen"},
		{[]string{"status", "--org", "a"}, "status takes no scope"},
		{[]string{"end", "--project", "x"}, "--project must be"},
	}
	for _, tc := range cases {
		_, err := runCmd(t, cfg, tc.args...)
		require.ErrorContains(t, err, tc.msg, tc.args)
	}
}

type outboxRow struct {
	event, email, subject string
	body                  []byte
}

func outboxRows(t *testing.T, dsn string) []outboxRow {
	t.Helper()
	conn, err := db.OpenSQLite(dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	rows, err := conn.Query(`SELECT event, email, subject, body FROM email_deliveries ORDER BY id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		require.NoError(t, rows.Scan(&r.event, &r.email, &r.subject, &r.body))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestMaintenanceCommandNotify(t *testing.T) {
	dsn := migratedDSN(t)
	conn, err := db.OpenSQLite(dsn)
	require.NoError(t, err)
	_, err = conn.Exec(`UPDATE users SET email = 'admin@example.com', email_verified = true WHERE id = 1`)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	cfg := &config.Config{DatabaseDSN: dsn, EmailSender: "log", EmailBaseURL: "https://nipa.example"}

	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	out, err := runCmd(t, cfg, "set", "read_only", "--start", start, "-m", "backup", "--notify")
	require.NoError(t, err)
	require.Contains(t, out, "notified: true (queued")
	require.NotContains(t, out, "note:")
	rows := outboxRows(t, dsn)
	require.Len(t, rows, 1)
	require.Equal(t, "maintenance.scheduled", rows[0].event)
	require.Equal(t, "admin@example.com", rows[0].email)
	require.Equal(t, "Maintenance scheduled: the Nipa server", rows[0].subject)
	msg, err := mail.DecodeMessage(rows[0].body)
	require.NoError(t, err)
	require.Contains(t, msg.Text, "Message: backup")
	require.Contains(t, msg.Text, "https://nipa.example/")

	out, err = runCmd(t, cfg, "set", "frozen", "--start", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "--notify")
	require.NoError(t, err)
	require.Contains(t, out, "note: email is paused while frozen")
	_, err = runCmd(t, cfg, "end")
	require.NoError(t, err)
	rows = outboxRows(t, dsn)
	require.Len(t, rows, 3)
	require.Equal(t, "maintenance.updated", rows[1].event)
	require.Equal(t, "maintenance.finished", rows[2].event)
	finished, err := mail.DecodeMessage(rows[2].body)
	require.NoError(t, err)
	require.Equal(t, msg.MessageID, finished.InReplyTo)

	_, err = runCmd(t, cfg, "set", "full", "--project", "default/default")
	require.NoError(t, err)
	_, err = runCmd(t, cfg, "end", "--project", "default/default")
	require.NoError(t, err)
	require.Len(t, outboxRows(t, dsn), 3, "a window stored without notify sends nothing when it ends")

	cfg.EmailSender = "off"
	out, err = runCmd(t, cfg, "set", "full", "--org", "default", "--notify")
	require.NoError(t, err)
	require.Contains(t, out, "notified: false")

	cfg.EmailSender = "smtp"
	out, err = runCmd(t, cfg, "set", "read_only", "--org", "default", "--notify")
	require.NoError(t, err)
	require.Contains(t, out, "warning: email disabled")
	require.Contains(t, out, "notified: false")
	require.Len(t, outboxRows(t, dsn), 3)

	for _, args := range [][]string{{"status"}, {"set", "full", "--org", "default"}} {
		out, err = runCmd(t, cfg, args...)
		require.NoError(t, err)
		require.NotContains(t, out, "email disabled", args[0])
	}
}
