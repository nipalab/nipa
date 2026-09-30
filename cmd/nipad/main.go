package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/webhook"
	webui "github.com/nipalab/nipa/web/server"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}

	dbConn, err := createDatabaseConnection(cfg.DatabaseDSN)
	if err != nil {
		panic(err)
	}

	slog.Info("migrating database...")
	err = db.MigrateUp(dbConn, "sqlite3")
	if err != nil {
		panic(err)
	}
	slog.Info("database migration completed")

	orgRepo := sqlite.NewOrgRepository(dbConn)
	projectRepo := sqlite.NewProjectRepository(dbConn)
	authRepo := sqlite.NewAuthRepository(dbConn)
	userRepo := sqlite.NewUserRepository(dbConn)
	branchRepository := sqlite.NewBranchRepository(dbConn)
	pushRepository := sqlite.NewPushRepository(dbConn)
	pbacRepository := sqlite.NewPBACRepository(dbConn)
	groupRepository := sqlite.NewGroupRepository(dbConn)

	passwordHasher := hasher.NewHasher(cfg.HasherWorkers)

	snowUser, err := snow.NewNode(cfg.SnowflakeNodeID)
	if err != nil {
		panic(err)
	}
	authUsecase := usecase.NewAuth(cfg.JWTKey, passwordHasher, userRepo, authRepo)
	orgUsecase := usecase.NewOrg(orgRepo)
	permissionUsecase := usecase.NewPermission(pbacRepository, userRepo, groupRepository, orgUsecase)
	projectUsecase := usecase.NewProject(projectRepo, snowUser, permissionUsecase, orgUsecase)
	chunkStore, err := createChunkStore(cfg)
	if err != nil {
		panic(fmt.Errorf("create chunk store: %w", err))
	}
	defer chunkStore.Close()
	if cfg.ChunkURLSigningKey == "" {
		panic("CHUNK_URL_SIGNING_KEY must be set")
	}
	branchUsecase := usecase.NewBranchWithChunks(permissionUsecase, branchRepository, snowUser, chunkStore)
	fileLockUsecase := usecase.NewFileLock(sqlite.NewFileLockRepository(dbConn), branchRepository, permissionUsecase, snowUser)
	branchUsecase = branchUsecase.WithFileLocks(fileLockUsecase)
	mergeRequestUsecase := usecase.NewMergeRequest(
		sqlite.NewMergeRequestRepository(dbConn),
		branchRepository,
		permissionUsecase,
		branchUsecase,
		snowUser,
	).WithFileLocks(fileLockUsecase)
	pushUsecase := usecase.NewPush(permissionUsecase, branchRepository, pushRepository, snowUser).WithFileLocks(fileLockUsecase)
	mergeRequestReviewUsecase := usecase.NewMergeRequestReview(
		sqlite.NewMergeRequestReviewRepository(dbConn),
		sqlite.NewMergeRequestRepository(dbConn),
		branchRepository,
		branchUsecase,
		permissionUsecase,
		userRepo,
		snowUser,
	)
	pushUsecase = pushUsecase.WithReviews(mergeRequestReviewUsecase)
	chunkUsecase := usecase.NewChunk(pushRepository, chunkStore, usecase.ChunkTransferConfig{
		SigningKey:  cfg.ChunkURLSigningKey,
		PresignTTL:  time.Duration(cfg.ChunkPresignTTLSeconds) * time.Second,
		MaxPageSize: cfg.ChunkMaxPageSize,
	})
	webhookRepository := sqlite.NewWebhookRepository(dbConn)
	webhookDispatcher := webhook.NewDispatcher(webhookRepository, webhook.NewClient(webhook.ClientConfig{
		Timeout:         time.Duration(cfg.WebhookTimeoutSeconds) * time.Second,
		EgressAllowlist: splitAllowlist(cfg.WebhookEgressAllowlist),
	}), snowUser, webhook.Config{})
	webhookUsecase := usecase.NewWebhook(webhookRepository, permissionUsecase, userRepo, webhookDispatcher, snowUser)
	hookEmitter := usecase.NewHookEmitter(webhookRepository, projectRepo, orgRepo, branchRepository, userRepo, webhookDispatcher)
	branchUsecase = branchUsecase.WithHooks(hookEmitter)
	mergeRequestUsecase = mergeRequestUsecase.WithHooks(hookEmitter)
	mergeRequestReviewUsecase = mergeRequestReviewUsecase.WithHooks(hookEmitter)
	pushUsecase = pushUsecase.WithHooks(hookEmitter)
	reg := &Registry{
		authUsecase:               authUsecase,
		userUsecase:               usecase.NewUser(snowUser, userRepo, passwordHasher),
		commonUsecase:             usecase.NewCommon(orgRepo, projectRepo),
		branchUsecase:             branchUsecase,
		pushUsecase:               pushUsecase,
		chunkUsecase:              chunkUsecase,
		permissionUsecase:         permissionUsecase,
		groupUsecase:              usecase.NewGroup(groupRepository, snowUser, permissionUsecase, orgUsecase),
		orgUsecase:                orgUsecase,
		projectUsecase:            projectUsecase,
		mergeRequestUsecase:       mergeRequestUsecase,
		mergeRequestReviewUsecase: mergeRequestReviewUsecase,
		fileLockUsecase:           fileLockUsecase,
		webhookUsecase:            webhookUsecase,
	}

	apiApp := api.NewAPI(reg)
	container := apiApp.SetupRoute()
	webhookDispatcher.Start()

	address := fmt.Sprintf("%s:%d", cfg.ServerAddress, cfg.ServerPort)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grpcInterceptor := server.NewInterceptor(reg.Auth())

	grpcRegistrar := grpc.NewServer(
		grpc.UnaryInterceptor(grpcInterceptor.JWTUnary()),
		grpc.StreamInterceptor(grpcInterceptor.JWTStream()),
	)
	grpcServer := server.New(reg)
	pb.RegisterNipaServiceServer(grpcRegistrar, grpcServer)

	webUI := webui.Handler()

	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("incoming connection", "content", r.Header.Get("Content-Type"), "method", r.Method, "url", r.URL.String(), "ProtoMajor", r.ProtoMajor)
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			slog.Info("grpc connection is coming")
			grpcRegistrar.ServeHTTP(w, r)
		} else if isAPIPath(r.URL.Path) {
			container.ServeHTTP(w, r)
		} else {
			webUI.ServeHTTP(w, r)
		}
	})

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	httpServer := &http.Server{Addr: address, Handler: mainHandler, Protocols: protocols}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		panic(err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		<-c
		slog.Info("shutting down server")
		if err := httpServer.Shutdown(ctx); err != nil {
			slog.Error("error shutting down server", "error", err)
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := webhookDispatcher.Stop(shutdownCtx); err != nil {
			slog.Warn("webhook dispatcher shutdown incomplete", "error", err)
		}
		os.Exit(0)
	}()

	slog.Info("server is running", "address", address)
	if err := httpServer.Serve(ln); err != http.ErrServerClosed {
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
		store, err := s3.New(context.Background(), s3.Config{
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
		return nil, fmt.Errorf("unknown CHUNK_STORAGE %q", cfg.ChunkStorage)
	}
}

func createDatabaseConnection(dsn string) (*sql.DB, error) {
	return db.OpenSQLite(dsn)
}

// splitAllowlist turns the comma-separated WEBHOOK_EGRESS_ALLOWLIST into
// entries. Empty means no egress restriction.
func splitAllowlist(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/auth") ||
		strings.HasPrefix(p, "/docs") ||
		strings.HasPrefix(p, "/api")
}
