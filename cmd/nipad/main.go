package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/webhook"
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
	orgUsecase := usecase.NewOrg(orgRepo, snowUser)
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
		dbtx.NewTransactor(dbConn),
	).WithFileLocks(fileLockUsecase)
	pushUsecase := usecase.NewPush(permissionUsecase, branchRepository, pushRepository, snowUser).WithFileLocks(fileLockUsecase)
	tagUsecase := usecase.NewTag(permissionUsecase, sqlite.NewTagRepository(dbConn), branchRepository, snowUser)
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
	mergeRequestUsecase = mergeRequestUsecase.WithReview(mergeRequestReviewUsecase)
	chunkUsecase := usecase.NewChunk(pushRepository, chunkStore, usecase.ChunkTransferConfig{
		SigningKey:  cfg.ChunkURLSigningKey,
		PresignTTL:  time.Duration(cfg.ChunkPresignTTLSeconds) * time.Second,
		MaxPageSize: cfg.ChunkMaxPageSize,
	})
	webhookRepository := sqlite.NewWebhookRepository(dbConn)
	webhookDispatcher := webhook.NewDispatcher(webhookRepository, webhook.NewClient(webhook.ClientConfig{
		Timeout:         time.Duration(cfg.WebhookTimeoutSeconds) * time.Second,
		EgressAllowlist: serverapp.SplitAllowlist(cfg.WebhookEgressAllowlist),
	}), snowUser, webhook.Config{})
	webhookUsecase := usecase.NewWebhook(webhookRepository, permissionUsecase, userRepo, webhookDispatcher, snowUser)
	hookEmitter := usecase.NewHookEmitter(webhookRepository, projectRepo, orgRepo, branchRepository, userRepo, webhookDispatcher)
	branchUsecase = branchUsecase.WithHooks(hookEmitter)
	tagUsecase = tagUsecase.WithHooks(hookEmitter)
	mergeRequestUsecase = mergeRequestUsecase.WithHooks(hookEmitter)
	mergeRequestReviewUsecase = mergeRequestReviewUsecase.WithHooks(hookEmitter)
	pushUsecase = pushUsecase.WithHooks(hookEmitter)

	reg := serverapp.NewRegistry(serverapp.Usecases{
		Auth:               authUsecase,
		User:               usecase.NewUser(snowUser, userRepo, passwordHasher),
		Common:             usecase.NewCommon(orgRepo, projectRepo),
		Branch:             branchUsecase,
		Tag:                tagUsecase,
		Push:               pushUsecase,
		Chunk:              chunkUsecase,
		Permission:         permissionUsecase,
		Group:              usecase.NewGroup(groupRepository, snowUser, permissionUsecase, orgUsecase),
		Org:                orgUsecase,
		Project:            projectUsecase,
		MergeRequest:       mergeRequestUsecase,
		MergeRequestReview: mergeRequestReviewUsecase,
		FileLock:           fileLockUsecase,
		Webhook:            webhookUsecase,
	})

	if err := serverapp.Run(cfg, reg, webhookDispatcher); err != nil {
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
