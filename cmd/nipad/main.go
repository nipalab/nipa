package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/version"
	"github.com/nipalab/nipa/internal/webhook"
)

func main() {
	if version.Flag(os.Args[1:]) {
		fmt.Println("nipad " + version.Resolve().String())
		return
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}

	if err := guardSecrets(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "nipad: "+err.Error())
		fmt.Fprintln(os.Stderr, "set strong values in config.yaml (openssl rand -base64 48) or ALLOW_INSECURE_DEV_SECRETS=true for local development only")
		os.Exit(1)
	}

	serviceName := cfg.TracingServiceName
	if serviceName == "" {
		serviceName = "nipad"
	}
	logsEndpoint := cfg.LogsOTLPEndpoint
	if logsEndpoint == "" {
		logsEndpoint = cfg.TracingOTLPEndpoint
	}
	logsProtocol := cfg.LogsOTLPProtocol
	if logsProtocol == "" {
		logsProtocol = cfg.TracingOTLPProtocol
	}
	logs, err := obs.SetupLogs(context.Background(), obs.LogsConfig{
		Enabled:        cfg.LogsOTLPEnabled,
		Endpoint:       logsEndpoint,
		Protocol:       logsProtocol,
		Headers:        obs.ParseHeaders(cfg.TracingOTLPHeaders),
		ServiceName:    serviceName,
		ServiceVersion: version.Resolve().Version,
	})
	if err != nil {
		panic(fmt.Errorf("setup logs: %w", err))
	}
	obs.Setup(cfg.LogLevel, cfg.LogFormat, "nipad", os.Stderr, logs.Handler())
	metrics := obs.NewMetrics()

	metricsEndpoint := cfg.MetricsOTLPEndpoint
	if metricsEndpoint == "" {
		metricsEndpoint = cfg.TracingOTLPEndpoint
	}
	metricsProtocol := cfg.MetricsOTLPProtocol
	if metricsProtocol == "" {
		metricsProtocol = cfg.TracingOTLPProtocol
	}
	metricsPush, err := obs.SetupMetricsPush(context.Background(), metrics, obs.MetricsPushConfig{
		Enabled:        cfg.MetricsOTLPEnabled,
		Endpoint:       metricsEndpoint,
		Protocol:       metricsProtocol,
		Headers:        obs.ParseHeaders(cfg.TracingOTLPHeaders),
		ServiceName:    serviceName,
		ServiceVersion: version.Resolve().Version,
		Interval:       time.Duration(cfg.MetricsOTLPIntervalSeconds) * time.Second,
	})
	if err != nil {
		panic(fmt.Errorf("setup metrics push: %w", err))
	}

	tracing, err := obs.SetupTracing(context.Background(), obs.TracingConfig{
		Enabled:        cfg.TracingEnabled,
		Endpoint:       cfg.TracingOTLPEndpoint,
		Protocol:       cfg.TracingOTLPProtocol,
		Headers:        obs.ParseHeaders(cfg.TracingOTLPHeaders),
		Stdout:         cfg.TracingStdout,
		SampleRatio:    cfg.TracingSampleRatio,
		ServiceName:    serviceName,
		ServiceVersion: version.Resolve().Version,
	})
	if err != nil {
		panic(fmt.Errorf("setup tracing: %w", err))
	}

	dbConn, err := createDatabaseConnection(cfg.DatabaseDSN)
	if err != nil {
		panic(err)
	}
	metrics.RegisterDB("sqlite", dbConn)

	slog.Info("migrating database...")
	err = db.MigrateUp(dbConn, "sqlite3")
	if err != nil {
		panic(err)
	}
	slog.Info("database migration completed")

	passwordHasher := hasher.NewHasher(cfg.HasherWorkers)

	snowUser, err := snow.NewNode(cfg.SnowflakeNodeID)
	if err != nil {
		panic(err)
	}
	slog.Info("using snowflake node id", "node_id", cfg.SnowflakeNodeID)

	chunkStore, err := createChunkStore(cfg)
	if err != nil {
		panic(fmt.Errorf("create chunk store: %w", err))
	}
	defer func() { _ = chunkStore.Close() }()
	if cfg.ChunkURLSigningKey == "" {
		panic("CHUNK_URL_SIGNING_KEY must be set")
	}

	webhookRepository := sqlite.NewWebhookRepository(dbConn)
	webhookDispatcher := webhook.NewDispatcher(webhookRepository, webhook.NewClient(webhook.ClientConfig{
		Timeout:         time.Duration(cfg.WebhookTimeoutSeconds) * time.Second,
		EgressAllowlist: serverapp.SplitAllowlist(cfg.WebhookEgressAllowlist),
	}), snowUser, webhook.Config{}).WithMetrics(metrics)

	emailRepository := sqlite.NewEmailRepository(dbConn)
	emailSender, err := mail.NewFromConfig(cfg.MailSenderConfig())
	if err != nil {
		panic(fmt.Errorf("email sender: %w", err))
	}
	var emailDispatcher *mail.Dispatcher
	if emailSender != nil {
		emailDispatcher = mail.NewDispatcher(emailRepository, emailSender, cfg.EmailDispatcherConfig()).WithMetrics(metrics)
	}

	rateLimiter, trustedProxies, err := serverapp.BuildRateLimiter(cfg, metrics, nil)
	if err != nil {
		panic(fmt.Errorf("rate limit: %w", err))
	}

	reg := serverapp.NewRegistry(serverapp.BuildUsecases(usecase.Repositories{
		Org:                sqlite.NewOrgRepository(dbConn),
		Project:            sqlite.NewProjectRepository(dbConn),
		Auth:               sqlite.NewAuthRepository(dbConn),
		SSHKey:             sqlite.NewSSHKeyRepository(dbConn),
		User:               sqlite.NewUserRepository(dbConn),
		Branch:             sqlite.NewBranchRepository(dbConn),
		Push:               sqlite.NewPushRepository(dbConn),
		PBAC:               sqlite.NewPBACRepository(dbConn),
		Group:              sqlite.NewGroupRepository(dbConn),
		FileLock:           sqlite.NewFileLockRepository(dbConn),
		MergeRequest:       sqlite.NewMergeRequestRepository(dbConn),
		MergeRequestReview: sqlite.NewMergeRequestReviewRepository(dbConn),
		MergeRequestCheck:  sqlite.NewMergeRequestCheckRepository(dbConn),
		Tag:                sqlite.NewTagRepository(dbConn),
		Webhook:            webhookRepository,
		Email:              emailRepository,
		Audit:              sqlite.NewAuditRepository(dbConn),
	}, serverapp.UsecaseDeps{
		Config:            cfg,
		DB:                dbConn,
		Snow:              snowUser,
		Hasher:            passwordHasher,
		ChunkStore:        chunkStore,
		Metrics:           metrics,
		WebhookDispatcher: webhookDispatcher,
		EmailDispatcher:   emailDispatcher,
		RateLimiter:       rateLimiter,
	}))

	dispatchers := []serverapp.Dispatcher{webhookDispatcher}
	if emailDispatcher != nil {
		dispatchers = append(dispatchers, emailDispatcher)
	}
	if err := serverapp.Run(cfg, reg, dispatchers, serverapp.WithReadiness(dbConn.PingContext), serverapp.WithMetrics(metrics), serverapp.WithTracing(tracing), serverapp.WithLogs(logs), serverapp.WithMetricsPush(metricsPush), serverapp.WithRateLimit(rateLimiter, trustedProxies)); err != nil {
		panic(err)
	}
}

func createChunkStore(cfg *config.Config) (storage.ChunkStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.ChunkStorage)) {
	case "", "local":
		store, err := storage.NewLocalStore(cfg.ChunkStorageDir)
		if err != nil {
			return nil, err
		}
		slog.Info("chunk storage ready", "backend", "local", "dir", cfg.ChunkStorageDir)
		return store, nil
	case "s3":
		return nil, fmt.Errorf("CHUNK_STORAGE=s3 requires the enterprise server (nipad-ee)")
	default:
		return nil, fmt.Errorf("unknown CHUNK_STORAGE %q", cfg.ChunkStorage)
	}
}

func createDatabaseConnection(dsn string) (*sql.DB, error) {
	return db.OpenSQLite(dsn)
}

func guardSecrets(cfg *config.Config) error {
	err := cfg.ValidateSecretsOrWarn(func(msg string, err error) {
		slog.Warn(msg, "problem", err.Error())
	})
	if err != nil {
		return err
	}
	if cfg.AllowInsecureDevSecrets {
		for _, key := range []*string{&cfg.JWTKey, &cfg.ChunkURLSigningKey} {
			if *key == "" {
				*key = rand.Text() + rand.Text()
			}
		}
	}
	return nil
}
