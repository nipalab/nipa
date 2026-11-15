package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/config"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/webhook"
)

func e2eMaintenanceOpener(cfg *config.Config) (serverusecase.Repositories, *sql.DB, error) {
	conn, err := db.OpenSQLite(cfg.DatabaseDSN)
	if err != nil {
		return serverusecase.Repositories{}, nil, err
	}
	return serverusecase.Repositories{
		Org:         sqlite.NewOrgRepository(conn),
		Project:     sqlite.NewProjectRepository(conn),
		PBAC:        sqlite.NewPBACRepository(conn),
		Audit:       sqlite.NewAuditRepository(conn),
		Maintenance: sqlite.NewMaintenanceRepository(conn),
	}, conn, nil
}

type countingSender struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (s *countingSender) Send(_ context.Context, msg mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return nil
}

func (s *countingSender) Close() error { return nil }

func (s *countingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// dataVersion reads PRAGMA data_version on a dedicated connection: it moves
// whenever another connection commits a write.
func dataVersion(t *testing.T, probe *sql.Conn) int64 {
	t.Helper()
	var v int64
	require.NoError(t, probe.QueryRowContext(context.Background(), "PRAGMA data_version").Scan(&v))
	return v
}

func TestEndToEnd_MaintenanceFrozen(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "nipa.db")
	dbConn, err := db.OpenSQLite(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, db.MigrateUp(dbConn, "sqlite3"))
	cfgDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("DATABASE_DSN: "+dsn+"\n"), 0o600))
	t.Chdir(cfgDir)
	t.Setenv("DATABASE_DSN", dsn)
	t.Setenv("MAINTENANCE_FILE", "")

	host, uc := startTestServerWithUsecases(t, dbConn)
	svc := uc.Maintenance.Service()
	poller := maintenance.NewPoller(svc, serverapp.MaintenanceLoader(sqlite.NewMaintenanceRepository(dbConn)), "", time.Hour)
	var resumes atomic.Int32
	failResume := atomic.Bool{}
	svc.OnResume(func(context.Context) error {
		resumes.Add(1)
		if failResume.Load() {
			return errors.New("lease taken")
		}
		return nil
	})
	command := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		handled, err := serverapp.RunMaintenanceCommand(append([]string{"maintenance"}, args...), e2eMaintenanceOpener, &out)
		require.True(t, handled)
		require.NoError(t, err)
		return out.String()
	}

	grpcClient, auth := newLoggedInClient(t, host)
	sc := func() pb.NipaServiceClient {
		c, err := grpcClient.ServiceClient()
		require.NoError(t, err)
		return c
	}
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	locks := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())
	push := func(dir, path string) error {
		writeFile(t, dir, path, path+"\n")
		stagePath(t, dir, path)
		return pusher.Run(ctx, dir, "add "+path)
	}
	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	require.NoError(t, push(dir, "a.txt"))

	expired := newMemoryStore()
	expiredTransport := clientgrpc.NewTransport()
	expiredClient := clientgrpc.NewClient(expiredTransport, clientusecase.NewSession(expired, expiredTransport, failPrompt{}))
	require.NoError(t, expiredClient.Connect(ctx, host))
	login, err := expiredClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	login.AccessToken = "expired"
	require.NoError(t, expired.SaveToken(login))

	_, project, err := uc.Common.ResolveBySlug(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	var hooks atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hooks.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)
	node, err := snow.NewNode(3)
	require.NoError(t, err)
	webhookRepo := sqlite.NewWebhookRepository(dbConn)
	hook, err := webhookRepo.Create(ctx, serverDomain.Webhook{ID: node.Generate(), ProjectID: project.ID, Name: "ci", URL: receiver.URL, Secret: "s", Events: []string{serverDomain.WebhookEventPush}, IsActive: true})
	require.NoError(t, err)
	now := time.Now()
	_, err = webhookRepo.CreateDelivery(ctx, serverDomain.WebhookDelivery{ID: node.Generate(), WebhookID: hook.ID, EventType: serverDomain.WebhookEventPush, Payload: []byte(`{}`), State: serverDomain.WebhookDeliveryPending, NextRetryAt: &now})
	require.NoError(t, err)
	body, err := mail.EncodeMessage(mail.Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)
	emailRepo := sqlite.NewEmailRepository(dbConn)
	require.NoError(t, emailRepo.Enqueue(ctx, []*serverDomain.EmailDelivery{{ID: node.Generate(), Event: "test", ProjectID: project.ID, UserID: 1, Email: "dev@example.com", Subject: "s", Body: body, State: serverDomain.EmailDeliveryPending}}))

	require.Contains(t, command("set", "frozen", "-m", "moving the database"), "frozen")
	require.NoError(t, poller.Poll(ctx))
	require.True(t, svc.Frozen(), "the server picks the frozen window up on its poll")

	webhooks := webhook.NewDispatcher(webhookRepo, webhook.NewClient(webhook.ClientConfig{EgressAllowlist: []string{"127.0.0.1"}}), node, webhook.Config{SweepInterval: 20 * time.Millisecond})
	sender := &countingSender{}
	mails := mail.NewDispatcher(emailRepo, sender, mail.DispatcherConfig{PollInterval: 20 * time.Millisecond})
	serverapp.PauseWhileFrozen(svc, webhooks, mails)
	webhooks.Start()
	mails.Start()
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = webhooks.Stop(stop)
		_ = mails.Stop(stop)
	})

	probeDB, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = probeDB.Close() })
	probe, err := probeDB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = probe.Close() })
	moved := filepath.Join(t.TempDir(), "moved.db")
	_, err = probe.ExecContext(ctx, "VACUUM INTO ?", moved)
	require.NoError(t, err)
	movedDB, err := db.OpenSQLite(moved)
	require.NoError(t, err)
	t.Cleanup(func() { _ = movedDB.Close() })
	movedSvc, movedPoller := serverapp.NewMaintenance(&config.Config{}, sqlite.NewMaintenanceRepository(movedDB), nil)
	require.NoError(t, movedPoller.Poll(ctx))
	require.True(t, movedSvc.Frozen(), "a copy of the database starts frozen")
	before := dataVersion(t, probe)

	clone := filepath.Join(t.TempDir(), "frozen-clone")
	require.NoError(t, repo.Clone(ctx, "http://"+host+"/"+e2eOrgSlug+"/"+e2eProjectSlug, host, e2eOrgSlug, e2eProjectSlug, "", nil, clone), "clone works while frozen")
	assertFileContent(t, clone, "a.txt", "a.txt\n")
	require.NoError(t, updater.Run(ctx, dir))
	_, err = grpcClient.GetCommitLog(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil, 10)
	require.NoError(t, err)
	_, _, err = grpcClient.ListMergeRequests(ctx, e2eOrgSlug, e2eProjectSlug, clientdomain.ListMergeRequestOptions{})
	require.NoError(t, err)
	_, err = grpcClient.ListFileLocks(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	_, err = sc().ListMyProjects(ctx, &pb.ListMyProjectsRequest{})
	require.NoError(t, err)
	status, err := sc().GetMaintenanceStatus(ctx, &pb.GetMaintenanceStatusRequest{})
	require.NoError(t, err)
	require.Equal(t, "frozen", status.GetServer().GetMode())

	err = push(dir, "b.txt")
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.Frozen)
	require.True(t, clientdomain.IsMaintenance(err), "the client maps the refusal")
	_, err = locks.Lock(ctx, dir, "art/x.png", clientusecase.LockOptions{})
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.Frozen)
	_, err = grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	requireMaintenance(t, err, maintenance.ScopeServer, maintenance.Frozen)
	require.Equal(t, http.StatusServiceUnavailable, rawChunk(t, http.MethodPut, host, e2eProjectSlug).StatusCode)

	_, err = expiredClient.ListFileLocks(ctx, e2eOrgSlug, e2eProjectSlug)
	require.True(t, clientdomain.IsMaintenance(err), "the refresh is refused for maintenance, got %v", err)
	kept, err := expired.LoadToken(host)
	require.NoError(t, err)
	require.Equal(t, login.RefreshToken, kept.RefreshToken, "the stored session is kept")

	time.Sleep(300 * time.Millisecond)
	require.Zero(t, hooks.Load(), "no webhook is delivered while frozen")
	require.Zero(t, sender.count(), "no email is sent while frozen")
	require.Equal(t, before, dataVersion(t, probe), "a frozen server writes nothing")

	failResume.Store(true)
	command("end")
	require.NotEqual(t, before, dataVersion(t, probe), "the probe sees writes")
	require.ErrorContains(t, poller.Poll(ctx), "lease taken")
	require.True(t, svc.Frozen(), "a failed resume keeps the server frozen")
	failResume.Store(false)
	require.NoError(t, poller.Poll(ctx))
	require.False(t, svc.Frozen())
	require.Equal(t, int32(2), resumes.Load())

	require.Eventually(t, func() bool { return hooks.Load() == 1 && sender.count() == 1 }, 10*time.Second, 20*time.Millisecond, "queued work is delivered after the window ends")
	_, err = expiredClient.ListFileLocks(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err, "the kept session refreshes once maintenance ended")
	require.NoError(t, push(dir, "c.txt"))
	require.Contains(t, command("status"), "off")
}
