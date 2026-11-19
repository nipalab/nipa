package e2e

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/config"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/serverapp"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

func TestEndToEnd_MaintenanceWindowsPropagate(t *testing.T) {
	dbConn := openTestDB(t)
	_, uc := startTestServerWithUsecases(t, dbConn)
	ctx := serverDomain.ContextWithClaim(context.Background(), serverDomain.Claims{UserID: 1, IsSuperAdmin: true})

	_, err := uc.Maintenance.SetServer(ctx, serverusecase.MaintenanceInput{Mode: "read_only", Message: "backup"})
	require.NoError(t, err)
	_, err = uc.Maintenance.SetProject(ctx, "default", "default", serverusecase.MaintenanceInput{Mode: "full"})
	require.NoError(t, err)

	w, scope := uc.Maintenance.Service().For("default", "default")
	require.Equal(t, maintenance.Full, w.Mode, "the writer applies its change locally")
	require.Equal(t, maintenance.ScopeProject, scope)

	other, poller := serverapp.NewMaintenance(&config.Config{}, sqlite.NewMaintenanceRepository(dbConn), nil)
	require.NoError(t, poller.Poll(context.Background()))
	w, scope = other.For("default", "default")
	require.Equal(t, maintenance.Full, w.Mode, "another instance catches up on its poll")
	require.Equal(t, maintenance.ScopeProject, scope)
	w, _ = other.For("", "")
	require.Equal(t, maintenance.ReadOnly, w.Mode)

	require.NoError(t, uc.Maintenance.EndProject(ctx, "default", "default"))
	require.NoError(t, uc.Maintenance.EndServer(ctx))
	require.NoError(t, poller.Poll(context.Background()))
	w, scope = other.For("default", "default")
	require.Equal(t, maintenance.Off, w.Mode)
	require.Empty(t, scope)
}

func requireMaintenance(t *testing.T, err error, scope maintenance.Scope, mode maintenance.Mode) {
	t.Helper()
	require.Error(t, err)
	refused, ok := maintenance.FromGRPC(err)
	require.True(t, ok, "expected a maintenance refusal, got %v", err)
	require.Equal(t, scope, refused.Info.Scope)
	require.Equal(t, mode, refused.Info.Mode)
}

func rawChunk(t *testing.T, method, host, project string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+host+"/api/chunks/"+e2eOrgSlug+"/"+project+"/abcd", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

func TestEndToEnd_MaintenanceEnforcement(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host, uc := startTestServerWithUsecases(t, dbConn)
	super := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: 1, IsAdmin: true, IsSuperAdmin: true})
	org, err := uc.Common.ResolveOrg(ctx, e2eOrgSlug)
	require.NoError(t, err)
	_, err = uc.Project.Create(super, org.ID, "Other", "", "other", nil)
	require.NoError(t, err)

	grpcClient, auth := newLoggedInClient(t, host)
	sc := func() pb.NipaServiceClient {
		c, err := grpcClient.ServiceClient()
		require.NoError(t, err)
		return c
	}
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	locks := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	cloneOf := func(project string) (string, error) {
		target := filepath.Join(t.TempDir(), project)
		url := "http://" + host + "/" + e2eOrgSlug + "/" + project
		return target, repo.Clone(ctx, url, host, e2eOrgSlug, project, "", nil, target)
	}
	push := func(dir, path string) error {
		writeFile(t, dir, path, path+"\n")
		stagePath(t, dir, path)
		return pusher.Run(ctx, dir, "add "+path)
	}

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	require.NoError(t, push(dir, "a.txt"))

	later := time.Now().Add(time.Hour)
	_, err = uc.Maintenance.SetServer(super, serverusecase.MaintenanceInput{Mode: "full", StartsAt: &later})
	require.NoError(t, err)
	require.NoError(t, push(dir, "b.txt"), "a scheduled window blocks nothing")

	now := time.Now().Add(-time.Second)
	_, err = uc.Maintenance.SetServer(super, serverusecase.MaintenanceInput{Mode: "read_only", Message: "backup", StartsAt: &now})
	require.NoError(t, err)
	_, err = cloneOf(e2eProjectSlug)
	require.NoError(t, err, "clone works in read-only")
	require.NoError(t, updater.Run(ctx, dir))
	requireMaintenance(t, push(dir, "c.txt"), maintenance.ScopeServer, maintenance.ReadOnly)
	_, err = locks.Lock(ctx, dir, "art/x.png", clientusecase.LockOptions{})
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.ReadOnly)
	_, err = grpcClient.AddMergeRequestComment(ctx, e2eOrgSlug, e2eProjectSlug, 1, "", nil, nil, "hi")
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.ReadOnly)
	put := rawChunk(t, http.MethodPut, host, e2eProjectSlug)
	require.Equal(t, http.StatusServiceUnavailable, put.StatusCode)
	require.Equal(t, "server;read_only", put.Header.Get("X-Nipa-Maintenance"))

	_, err = uc.Maintenance.SetServer(super, serverusecase.MaintenanceInput{Mode: "full"})
	require.NoError(t, err)
	_, err = cloneOf(e2eProjectSlug)
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.Full)
	_, err = sc().ListMyProjects(ctx, &pb.ListMyProjectsRequest{})
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.Full)
	require.Equal(t, http.StatusServiceUnavailable, rawChunk(t, http.MethodGet, host, e2eProjectSlug).StatusCode)
	_, err = grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err, "sign-in stays open")
	status, err := sc().GetMaintenanceStatus(ctx, &pb.GetMaintenanceStatusRequest{Context: &pb.ProjectContext{Org: e2eOrgSlug, Project: e2eProjectSlug}})
	require.NoError(t, err)
	require.Equal(t, "full", status.GetServer().GetMode())
	require.NoError(t, uc.Maintenance.EndServer(super))

	_, err = uc.Maintenance.SetProject(super, e2eOrgSlug, e2eProjectSlug, serverusecase.MaintenanceInput{Mode: "read_only"})
	require.NoError(t, err)
	requireMaintenance(t, push(dir, "d.txt"), maintenance.ScopeProject, maintenance.ReadOnly)
	other, err := cloneOf("other")
	require.NoError(t, err)
	require.NoError(t, push(other, "o.txt"), "another project is untouched")

	_, err = uc.Maintenance.SetProject(super, e2eOrgSlug, e2eProjectSlug, serverusecase.MaintenanceInput{Mode: "full"})
	require.NoError(t, err)
	requireMaintenance(t, updater.Run(ctx, dir), maintenance.ScopeProject, maintenance.Full)
	projects, err := sc().ListMyProjects(ctx, &pb.ListMyProjectsRequest{})
	require.NoError(t, err)
	var slugs []string
	for _, p := range projects.GetProjects() {
		slugs = append(slugs, p.GetOrg()+"/"+p.GetProject())
	}
	require.Contains(t, slugs, e2eOrgSlug+"/"+e2eProjectSlug, "a closed project is still listed")
	require.NoError(t, uc.Maintenance.EndProject(super, e2eOrgSlug, e2eProjectSlug))

	_, err = uc.Maintenance.SetOrg(super, e2eOrgSlug, serverusecase.MaintenanceInput{Mode: "read_only"})
	require.NoError(t, err)
	requireMaintenance(t, push(other, "p.txt"), maintenance.ScopeOrg, maintenance.ReadOnly)
	_, err = sc().CreateGroup(ctx, &pb.CreateGroupRequest{Org: e2eOrgSlug, Name: "artists"})
	requireMaintenance(t, err, maintenance.ScopeOrg, maintenance.ReadOnly)
	require.NoError(t, uc.Maintenance.EndOrg(super, e2eOrgSlug))

	require.NoError(t, push(dir, "e.txt"), "pushes pass once maintenance ended")
}

func TestEndToEnd_MaintenanceAnnouncements(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	sender := &countingSender{}
	mails := mail.NewDispatcher(sqlite.NewEmailRepository(dbConn), sender, mail.DispatcherConfig{PollInterval: time.Hour})
	_, uc := startTestServerWithUsecases(t, dbConn, func(d *serverapp.UsecaseDeps) {
		d.EmailDispatcher = mails
		d.Config.EmailBaseURL = "https://nipa.example"
	})
	mails.Start()
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = mails.Stop(stop)
	})

	super := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: 1, IsAdmin: true, IsSuperAdmin: true})
	org, err := uc.Common.ResolveOrg(ctx, e2eOrgSlug)
	require.NoError(t, err)
	other, err := uc.Project.Create(super, org.ID, "Other", "", "other", nil)
	require.NoError(t, err)
	_, game, err := uc.Common.ResolveBySlug(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	users := []struct {
		id       int64
		email    string
		optedOut bool
	}{
		{60, "owner@example.com", false},
		{61, "game-admin@example.com", false},
		{62, "other-admin@example.com", false},
		{63, "org-admin@example.com", false},
		{64, "quiet-owner@example.com", true},
		{65, "beta-owner@example.com", false},
		{66, "writer@example.com", false},
	}
	for _, u := range users {
		_, err := dbConn.ExecContext(ctx, `INSERT INTO users (id, name, email, password, notify_email) VALUES (?, ?, ?, '', ?)`, u.id, u.email, u.email, !u.optedOut)
		require.NoError(t, err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := dbConn.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO org_members (org_id, user_id, role) VALUES (?, 60, 'owner'), (?, 64, 'owner'), (?, 66, 'member')`, org.ID.Int64(), org.ID.Int64(), org.ID.Int64())
	rule := `INSERT INTO pbac_rules (user_id, org_id, project_id, path_prefix, permission) VALUES (?, ?, ?, '', ?)`
	exec(rule, 61, org.ID.Int64(), game.ID.Int64(), int64(serverDomain.PermissionAll))
	exec(rule, 62, org.ID.Int64(), other.ID.Int64(), int64(serverDomain.PermissionAll))
	exec(rule, 63, org.ID.Int64(), nil, int64(serverDomain.PermissionAll))
	exec(rule, 66, org.ID.Int64(), game.ID.Int64(), int64(serverDomain.PermissionRead|serverDomain.PermissionWrite))
	beta := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: 65})
	_, err = uc.Org.Create(beta, "Beta", "beta")
	require.NoError(t, err)

	inbox := func(n int) map[string]mail.Message {
		t.Helper()
		require.Eventually(t, func() bool { return sender.count() >= n }, 10*time.Second, 20*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		sender.mu.Lock()
		defer sender.mu.Unlock()
		require.Len(t, sender.sent, n)
		out := make(map[string]mail.Message, n)
		for _, msg := range sender.sent {
			out[msg.To[0]] = msg
		}
		sender.sent = nil
		return out
	}
	addresses := func(m map[string]mail.Message) []string {
		out := make([]string, 0, len(m))
		for to := range m {
			out = append(out, to)
		}
		sort.Strings(out)
		return out
	}

	later := time.Now().Add(time.Hour)
	st, err := uc.Maintenance.SetProject(super, e2eOrgSlug, e2eProjectSlug, serverusecase.MaintenanceInput{Mode: "read_only", Message: "asset repack", StartsAt: &later, Notify: true})
	require.NoError(t, err)
	require.True(t, *st.Notified)
	got := inbox(4)
	require.Equal(t, []string{"game-admin@example.com", "org-admin@example.com", "owner@example.com", "quiet-owner@example.com"}, addresses(got))
	scheduled := got["owner@example.com"]
	require.Equal(t, "Maintenance scheduled: project default/default", scheduled.Subject)
	require.Contains(t, scheduled.Text, "Message: asset repack")
	require.Contains(t, scheduled.Text, "https://nipa.example/default/default")

	_, err = uc.Maintenance.SetProject(super, e2eOrgSlug, e2eProjectSlug, serverusecase.MaintenanceInput{Mode: "full", Notify: true})
	require.NoError(t, err)
	updated := inbox(4)["owner@example.com"]
	require.Equal(t, "Maintenance updated: project default/default", updated.Subject)
	require.Equal(t, scheduled.MessageID, updated.InReplyTo, "updates reply to the first announcement")
	require.NoError(t, uc.Maintenance.EndProject(super, e2eOrgSlug, e2eProjectSlug))
	require.Equal(t, "Maintenance finished: project default/default", inbox(4)["owner@example.com"].Subject)

	st, err = uc.Maintenance.SetOrg(super, e2eOrgSlug, serverusecase.MaintenanceInput{Mode: "full", Notify: true})
	require.NoError(t, err)
	require.True(t, *st.Notified)
	got = inbox(5)
	require.Equal(t, []string{"game-admin@example.com", "org-admin@example.com", "other-admin@example.com", "owner@example.com", "quiet-owner@example.com"}, addresses(got))
	require.Equal(t, "Maintenance started: organization Default Organization (default)", got["owner@example.com"].Subject)
	require.NoError(t, uc.Maintenance.EndOrg(super, e2eOrgSlug))
	inbox(5)

	st, err = uc.Maintenance.SetServer(super, serverusecase.MaintenanceInput{Mode: "read_only", StartsAt: &later, Notify: true})
	require.NoError(t, err)
	require.True(t, *st.Notified)
	got = inbox(3)
	require.Equal(t, []string{"beta-owner@example.com", "owner@example.com", "quiet-owner@example.com"}, addresses(got))
	require.Equal(t, "Maintenance scheduled: the Nipa server", got["beta-owner@example.com"].Subject)

	st, err = uc.Maintenance.SetServer(super, serverusecase.MaintenanceInput{Mode: "read_only"})
	require.NoError(t, err)
	require.False(t, *st.Notified, "notify false sends nothing")
	require.NoError(t, uc.Maintenance.EndServer(super))
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, sender.count(), "a window last stored without notify ends silently")

	require.Eventually(t, func() bool {
		var queued int
		err := dbConn.QueryRowContext(ctx, `SELECT COUNT(*) FROM email_deliveries WHERE event LIKE 'maintenance.%' AND state = 'delivered'`).Scan(&queued)
		return err == nil && queued == 25
	}, 10*time.Second, 50*time.Millisecond)
}

func TestEndToEnd_MaintenanceAnnouncementFromCLI(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "nipa.db")
	dbConn, err := db.OpenSQLite(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, db.MigrateUp(dbConn, "sqlite3"))
	_, err = dbConn.ExecContext(ctx, `INSERT INTO users (id, name, email, password) VALUES (70, 'Owner', 'owner@example.com', '')`)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx, `INSERT INTO org_members (org_id, user_id, role) SELECT id, 70, 'owner' FROM organizations WHERE slug = ?`, e2eOrgSlug)
	require.NoError(t, err)

	sender := &countingSender{}
	mails := mail.NewDispatcher(sqlite.NewEmailRepository(dbConn), sender, mail.DispatcherConfig{PollInterval: 50 * time.Millisecond})
	startTestServerWithUsecases(t, dbConn, func(d *serverapp.UsecaseDeps) { d.EmailDispatcher = mails })
	mails.Start()
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = mails.Stop(stop)
	})

	cfgDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("DATABASE_DSN: "+dsn+"\nEMAIL_SENDER: log\n"), 0o600))
	t.Chdir(cfgDir)
	t.Setenv("DATABASE_DSN", dsn)
	t.Setenv("MAINTENANCE_FILE", "")
	var out bytes.Buffer
	handled, err := serverapp.RunMaintenanceCommand([]string{"maintenance", "set", "read_only", "--project", e2eOrgSlug + "/" + e2eProjectSlug, "-m", "repack", "--notify"}, e2eMaintenanceOpener, &out)
	require.True(t, handled)
	require.NoError(t, err)
	require.Contains(t, out.String(), "notified: true")

	require.Eventually(t, func() bool { return sender.count() == 1 }, 10*time.Second, 20*time.Millisecond, "the server's dispatcher sends what the command queued")
	sender.mu.Lock()
	msg := sender.sent[0]
	sender.mu.Unlock()
	require.Equal(t, []string{"owner@example.com"}, msg.To)
	require.Equal(t, "Maintenance started: project default/default", msg.Subject)
}
