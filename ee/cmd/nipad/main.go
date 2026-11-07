package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/emicklei/go-restful/v3"

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/nodelease"
	"github.com/nipalab/nipa/ee/oauth"
	eeratelimit "github.com/nipalab/nipa/ee/ratelimit"
	"github.com/nipalab/nipa/ee/registration"
	"github.com/nipalab/nipa/ee/repository/postgres"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/ee/web/marketing"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/version"
	"github.com/nipalab/nipa/internal/webhook"
)

func main() {
	if version.Flag(os.Args[1:]) {
		fmt.Println("nipad-ee " + version.Resolve().String())
		return
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}
	if err := cfg.ValidateSecrets(); err != nil {
		panic(err)
	}

	serviceName := cfg.TracingServiceName
	if serviceName == "" {
		serviceName = "nipad-ee"
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
	obs.Setup(cfg.LogLevel, cfg.LogFormat, "nipad-ee", os.Stderr, logs.Handler())
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

	dbConn, err := eedb.Open(cfg.DatabaseDSN)
	if err != nil {
		panic(err)
	}
	metrics.RegisterDB("postgres", dbConn)

	slog.Info("migrating database...")
	if err := eedb.MigrateUp(dbConn); err != nil {
		panic(err)
	}
	slog.Info("database migration completed")

	userRepo := postgres.NewUserRepository(dbConn)
	passwordHasher := hasher.NewHasher(cfg.HasherWorkers)

	snowUser, leaseManager, err := resolveSnowflakeNode(cfg, dbConn)
	if err != nil {
		panic(err)
	}
	if leaseManager != nil {
		defer leaseManager.Release(context.Background())
	}

	chunkStore, err := createChunkStore(cfg)
	if err != nil {
		panic(fmt.Errorf("create chunk store: %w", err))
	}
	defer func() { _ = chunkStore.Close() }()

	storageLedger, err := createStorageLedger(cfg, dbConn)
	if err != nil {
		panic(err)
	}

	webhookRepository := postgres.NewWebhookRepository(dbConn)
	webhookDispatcher := webhook.NewDispatcher(webhookRepository, webhook.NewClient(webhook.ClientConfig{
		Timeout:         time.Duration(cfg.WebhookTimeoutSeconds) * time.Second,
		EgressAllowlist: serverapp.SplitAllowlist(cfg.WebhookEgressAllowlist),
	}), snowUser, webhook.Config{}).WithMetrics(metrics)

	emailRepository := postgres.NewEmailRepository(dbConn)
	emailSender, err := mail.NewFromConfig(cfg.MailSenderConfig())
	if err != nil {
		panic(fmt.Errorf("email sender: %w", err))
	}
	var emailDispatcher *mail.Dispatcher
	if emailSender != nil {
		emailDispatcher = mail.NewDispatcher(emailRepository, emailSender, cfg.EmailDispatcherConfig()).WithMetrics(metrics)
	}

	rateLimiter, trustedProxies, err := serverapp.BuildRateLimiter(cfg, metrics, newRedisRateLimitStore)
	if err != nil {
		panic(fmt.Errorf("rate limit: %w", err))
	}

	fileLockRepo := postgres.NewFileLockRepository(dbConn)
	deps := serverapp.UsecaseDeps{
		Config:            cfg,
		DB:                dbConn,
		Snow:              snowUser,
		Hasher:            passwordHasher,
		ChunkStore:        chunkStore,
		Metrics:           metrics,
		WebhookDispatcher: webhookDispatcher,
		EmailDispatcher:   emailDispatcher,
		RateLimiter:       rateLimiter,
	}
	if storageLedger != nil {
		deps.StorageLedger = storageLedger
		slog.Info("storage ledger enabled", "default_quota_bytes", cfg.StorageLedgerDefaultQuotaBytes)
	}
	uc := serverapp.BuildUsecases(usecase.Repositories{
		Org:                postgres.NewOrgRepository(dbConn),
		Project:            postgres.NewProjectRepository(dbConn),
		Auth:               postgres.NewAuthRepository(dbConn),
		SSHKey:             postgres.NewSSHKeyRepository(dbConn),
		User:               userRepo,
		Branch:             postgres.NewBranchRepository(dbConn),
		Push:               postgres.NewPushRepository(dbConn),
		PBAC:               postgres.NewPBACRepository(dbConn),
		Group:              postgres.NewGroupRepository(dbConn),
		FileLock:           fileLockRepo,
		FileLockSerializer: fileLockRepo,
		MergeRequest:       postgres.NewMergeRequestRepository(dbConn),
		MergeRequestReview: postgres.NewMergeRequestReviewRepository(dbConn),
		MergeRequestCheck:  postgres.NewMergeRequestCheckRepository(dbConn),
		Tag:                postgres.NewTagRepository(dbConn),
		Webhook:            webhookRepository,
		Email:              emailRepository,
	}, deps)
	reg := serverapp.NewRegistry(uc)
	commonUsecase, orgUsecase, authUsecase := uc.Common, uc.Org, uc.Auth

	dispatchers := []serverapp.Dispatcher{webhookDispatcher}
	if emailDispatcher != nil {
		dispatchers = append(dispatchers, emailDispatcher)
	}
	runOpts := []serverapp.Option{serverapp.WithReadiness(dbConn.PingContext), serverapp.WithMetrics(metrics), serverapp.WithTracing(tracing), serverapp.WithLogs(logs), serverapp.WithMetricsPush(metricsPush), serverapp.WithRateLimit(rateLimiter, trustedProxies)}
	if storageLedger != nil {
		runOpts = append(runOpts, serverapp.WithRoutes(func(ws *restful.WebService) {
			storageusage.RegisterRoutes(ws, storageusage.RoutesConfig{
				Ledger: storageLedger,
				Common: commonUsecase,
				Orgs:   orgUsecase,
			})
		}))
	}
	if cfg.SaaSMode {
		runOpts = append(runOpts, saasOptions(cfg, snowUser, passwordHasher, userRepo, authUsecase, emailRepository, emailDispatcher, dbConn)...)
	}
	if err := serverapp.Run(cfg, reg, dispatchers, runOpts...); err != nil {
		panic(err)
	}
}

// saasOptions wires the self-registration and social login surfaces of the
// SaaS deployment. Both use the same onboarding repository, which runs inside
// the caller's transaction.
func saasOptions(cfg *config.Config, snowUser snow.Node, hasher *hasher.Hasher, userRepo *postgres.User,
	authUsecase *usecase.Auth, emailRepository *postgres.EmailRepository, emailDispatcher *mail.Dispatcher, dbConn *sql.DB,
) []serverapp.Option {
	if strings.TrimSpace(cfg.EmailBaseURL) == "" {
		panic("SAAS_MODE requires EMAIL_BASE_URL: the confirmation link has no public base URL")
	}
	registrationRepo := postgres.NewRegistrationRepository(dbConn)
	transactor := dbtx.NewTransactor(dbConn)

	registrationUsecase := registration.New(snowUser, hasher, userRepo, registrationRepo, transactor, registration.Config{
		FreeQuotaBytes: cfg.SaasFreeQuotaBytes,
		BaseURL:        cfg.EmailBaseURL,
	})
	registrationHandler := registration.NewHandler(registrationUsecase, authUsecase)
	if emailDispatcher != nil {
		registrationUsecase.WithMailer(emailRepository, emailDispatcher)
	} else {
		slog.Warn("SAAS_MODE with EMAIL_SENDER=off: confirmation emails will not be sent")
	}
	options := []serverapp.Option{
		serverapp.WithMarketingUI(marketing.Handler()),
		serverapp.WithPublicRoutes(func(ws *restful.WebService) {
			registration.RegisterPublicRoutes(ws, registrationHandler)
		}),
	}
	slog.Info("saas self-registration enabled", "free_quota_bytes", cfg.SaasFreeQuotaBytes)
	slog.Info("saas marketing fallback enabled", "base_path", marketing.BasePath)

	providers := oauth.NewProviders(oauth.ProvidersConfig{
		BaseURL:            cfg.OAuthBaseURL,
		GoogleClientID:     cfg.OAuthGoogleClientID,
		GoogleClientSecret: cfg.OAuthGoogleClientSecret,
		GitHubClientID:     cfg.OAuthGitHubClientID,
		GitHubClientSecret: cfg.OAuthGitHubClientSecret,
	})
	oauthUsecase := oauth.New(providers, cfg.JWTKey, userRepo, registrationRepo, transactor, snowUser, oauth.Config{
		FreeQuotaBytes: cfg.SaasFreeQuotaBytes,
	})
	registrationHandler = registrationHandler.WithProviders(oauthUsecase.Providers)
	oauthHandler := oauth.NewHandler(oauthUsecase, authUsecase)
	options = append(options,
		serverapp.WithPublicRoutes(func(ws *restful.WebService) {
			oauth.RegisterPublicRoutes(ws, oauthHandler)
		}),
		serverapp.WithRoutes(func(ws *restful.WebService) {
			oauth.RegisterRoutes(ws, oauthHandler)
		}),
	)
	if len(providers) > 0 {
		slog.Info("social login enabled", "providers", oauthUsecase.Providers())
	} else {
		slog.Info("social login routes enabled without providers: set OAUTH_* client credentials to enable a provider")
	}
	return options
}

func resolveSnowflakeNode(cfg *config.Config, dbConn *sql.DB) (snow.Node, *nodelease.Manager, error) {
	if cfg.SnowflakeNodeIDSet {
		node, err := snow.NewNode(cfg.SnowflakeNodeID)
		if err != nil {
			return nil, nil, err
		}
		slog.Info("using configured snowflake node id", "node_id", cfg.SnowflakeNodeID)
		return node, nil, nil
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve hostname for snowflake node lease: %w", err)
	}
	manager, err := nodelease.New(nodelease.Config{
		DB:     dbConn,
		Holder: hostname,
		TTL:    time.Duration(cfg.SnowflakeLeaseTTLSecs) * time.Second,
	})
	if err != nil {
		return nil, nil, err
	}
	node, err := manager.Acquire(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("acquire snowflake node lease: %w", err)
	}
	return node, manager, nil
}

func createStorageLedger(cfg *config.Config, dbConn *sql.DB) (*storageusage.Ledger, error) {
	setting := strings.ToLower(strings.TrimSpace(cfg.StorageLedger))
	if setting == "" && cfg.SaaSMode {
		slog.Info("SAAS_MODE enables the cloud storage ledger so seeded quotas are enforced")
		setting = "cloud"
	}
	switch setting {
	case "":
		return nil, nil
	case "cloud":
		return storageusage.NewLedger(dbConn, storageusage.Config{
			DefaultQuotaBytes: cfg.StorageLedgerDefaultQuotaBytes,
		}), nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_LEDGER %q", cfg.StorageLedger)
	}
}

func createChunkStore(cfg *config.Config) (storage.ChunkStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.ChunkStorage)) {
	case "", "s3":
		store, err := s3store.New(context.Background(), s3store.Config{
			Endpoint:        cfg.ChunkS3Endpoint,
			Region:          cfg.ChunkS3Region,
			Bucket:          cfg.ChunkS3Bucket,
			Prefix:          cfg.ChunkS3Prefix,
			AccessKeyID:     cfg.ChunkS3AccessKeyID,
			SecretAccessKey: cfg.ChunkS3SecretAccessKey,
		})
		if err != nil {
			return nil, err
		}
		slog.Info("chunk storage ready", "backend", "s3", "endpoint", cfg.ChunkS3Endpoint, "bucket", cfg.ChunkS3Bucket)
		return store, nil
	default:
		return nil, fmt.Errorf("enterprise server only supports CHUNK_STORAGE=s3, got %q", cfg.ChunkStorage)
	}
}

func newRedisRateLimitStore(cfg *config.Config) (ratelimit.Store, error) {
	return eeratelimit.New(context.Background(), eeratelimit.Config{
		Addr:     cfg.RateLimitRedisAddr,
		Password: cfg.RateLimitRedisPassword,
		DB:       cfg.RateLimitRedisDB,
		TLS:      cfg.RateLimitRedisTLS,
		Prefix:   cfg.RateLimitRedisPrefix,
	})
}
