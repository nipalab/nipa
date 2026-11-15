package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/version"
)

func readOnlyWindows() *domain.MaintenanceStatus {
	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	return &domain.MaintenanceStatus{
		Server:  &domain.MaintenanceWindow{Mode: "read_only", Active: true, Message: "storage move", EndsAt: &ends},
		Project: &domain.MaintenanceWindow{Mode: "full", StartsAt: ends.Add(time.Hour), Org: "org", Project: "org/project"},
	}
}

func TestSetupVersionCmd_ServerMaintenance(t *testing.T) {
	pinVersion(t, "v1.2.3", "abc1234")
	client := &fakeVersionClient{result: &version.Info{Version: "v1.2.3"}, windows: readOnlyWindows()}
	out, err := runCmdInDir(t, t.TempDir(), newVersionCli(client).setupVersionCmd(), "--server", "http://example.com/org/project")
	require.NoError(t, err)
	require.Equal(t, "org", client.org)
	require.Equal(t, "project", client.project)
	require.Contains(t, out, "maintenance: server read-only until ~2026-11-15 14:00 UTC: storage move\n")
	require.Contains(t, out, "maintenance: project org/project closed from 2026-11-15 15:00 UTC (scheduled)\n")

	out, err = runCmdInDir(t, t.TempDir(), newVersionCli(client).setupVersionCmd(), "--server", "http://example.com/org/project", "--json")
	require.NoError(t, err)
	var parsed versionPair
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "read_only", parsed.Maintenance.Server.Mode)

	out, err = runCmdInDir(t, t.TempDir(), newVersionCli(&fakeVersionClient{result: &version.Info{Version: "v1.2.3"}}).setupVersionCmd(), "--server", "http://example.com/org/project")
	require.NoError(t, err)
	require.Contains(t, out, "maintenance: off\n")

	out, err = runCmdInDir(t, t.TempDir(), newVersionCli(&fakeVersionClient{result: &version.Info{Version: "v1.2.3"}, windowsErr: errors.New("unimplemented")}).setupVersionCmd(), "--server", "http://example.com/org/project")
	require.NoError(t, err)
	require.NotContains(t, out, "maintenance:", "an older server says nothing")
}

func TestSetupStatusCmd_Maintenance(t *testing.T) {
	root := setupRepoWithURL(t, "http://nipa.internal/org/project")
	client := &fakeVersionClient{windows: readOnlyWindows()}
	cli := newVersionCli(client)

	out, err := runCmdInDir(t, root, cli.setupStatusCmd())
	require.NoError(t, err)
	require.Equal(t, "http://nipa.internal", client.host)
	require.Contains(t, out, "On branch main\nmaintenance: server read-only until ~2026-11-15 14:00 UTC: storage move\n")

	out, err = runCmdInDir(t, root, cli.setupStatusCmd(), "--json")
	require.NoError(t, err)
	var parsed output.Status
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "org/project", parsed.Maintenance.Project.Project)

	out, err = runCmdInDir(t, root, newVersionCli(&fakeVersionClient{windowsErr: errors.New("offline")}).setupStatusCmd())
	require.NoError(t, err)
	require.NotContains(t, out, "maintenance", "a failed probe never fails status")
}

func TestSetupPushCmd_MaintenanceRefusesBeforeWork(t *testing.T) {
	root := setupRepoWithURL(t, "http://nipa.internal/org/project")
	cli := newVersionCli(&fakeVersionClient{windows: readOnlyWindows()})

	_, err := runCmdInDir(t, root, cli.setupPushCmd(), "-m", "change")
	require.True(t, domain.IsMaintenance(err), "got %v", err)
	require.Equal(t, "server is in maintenance (read-only): storage move", err.Error())
}

func TestMaintenanceGate(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	require.NoError(t, maintenanceGate(&buf, nil, now))
	require.Empty(t, buf.String())

	soon := &domain.MaintenanceStatus{Org: &domain.MaintenanceWindow{Mode: "read_only", Org: "org", StartsAt: now.Add(12 * time.Minute)}}
	require.NoError(t, maintenanceGate(&buf, soon, now))
	require.Equal(t, "maintenance starts in 12m: organization org read-only from 2026-11-15 12:12 UTC (scheduled)\n", buf.String())

	buf.Reset()
	later := &domain.MaintenanceStatus{Server: &domain.MaintenanceWindow{Mode: "full", StartsAt: now.Add(48 * time.Hour)}}
	require.NoError(t, maintenanceGate(&buf, later, now))
	require.Empty(t, buf.String(), "windows more than a day away are not announced")

	require.Equal(t, "less than a minute", roughDuration(20*time.Second))
	require.Equal(t, "2h5m", roughDuration(2*time.Hour+5*time.Minute+10*time.Second))
}

func TestErrorText(t *testing.T) {
	require.Equal(t, "plain", ErrorText(errors.New("plain")))
	refused := domain.NewMaintenanceError(maintenance.Info{Scope: maintenance.ScopeServer, Mode: maintenance.ReadOnly, Message: "move"}, nil)
	require.Equal(t, "push: server is in maintenance (read-only): move\nhint: read-only commands still work", ErrorText(fmt.Errorf("push: %w", refused)))
}

func TestCloneMaintenance_NoUsecases(t *testing.T) {
	root := setupRepoWithURL(t, "http://nipa.internal/org/project")
	cmd := (&Cli{useCase: &fakeUsecaseContainer{}}).setupStatusCmd()
	require.Nil(t, (&Cli{useCase: &fakeUsecaseContainer{}}).cloneMaintenance(cmd, root))
	require.Nil(t, (&Cli{}).cloneMaintenance(cmd, root))
	require.Nil(t, newVersionCli(&fakeVersionClient{}).cloneMaintenance(cmd, t.TempDir()), "no clone config")
}
