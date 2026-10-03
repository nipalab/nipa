package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/repository/dbtx"
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

	dbConn, err := eedb.Open(cfg.DatabaseDSN)
	if err != nil {
		panic(err)
	}

	slog.Info("migrating database...")
	if err := eedb.MigrateUp(dbConn); err != nil {
		panic(err)
	}
	slog.Info("database migration completed")

	orgRepo := postgres.NewOrgRepository(dbConn)
	projectRepo := postgres.NewProjectRepository(dbConn)
	authRepo := postgres.NewAuthRepository(dbConn)
	userRepo := postgres.NewUserRepository(dbConn)
	branchRepository := postgres.NewBranchRepository(dbConn)
	pushRepository := postgres.NewPushRepository(dbConn)
	pbacRepository := postgres.NewPBACRepository(dbConn)
	groupRepository := postgres.NewGroupRepository(dbConn)

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
	defer func() { _ = chunkStore.Close() }()
	if cfg.ChunkURLSigningKey == "" {
		panic("CHUNK_URL_SIGNING_KEY must be set")
	}
	branchUsecase := usecase.NewBranchWithChunks(permissionUsecase, branchRepository, snowUser, chunkStore)
	fileLockUsecase := usecase.NewFileLock(postgres.NewFileLockRepository(dbConn), branchRepository, permissionUsecase, snowUser)
	branchUsecase = branchUsecase.WithFileLocks(fileLockUsecase)
	mergeRequestUsecase := usecase.NewMergeRequest(
		postgres.NewMergeRequestRepository(dbConn),
		branchRepository,
		permissionUsecase,
		branchUsecase,
		snowUser,
		dbtx.NewTransactor(dbConn),
	).WithFileLocks(fileLockUsecase)
	pushUsecase := usecase.NewPush(permissionUsecase, branchRepository, pushRepository, snowUser).WithFileLocks(fileLockUsecase)
	tagUsecase := usecase.NewTag(permissionUsecase, postgres.NewTagRepository(dbConn), branchRepository, snowUser)
	mergeRequestReviewUsecase := usecase.NewMergeRequestReview(
		postgres.NewMergeRequestReviewRepository(dbConn),
		postgres.NewMergeRequestRepository(dbConn),
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
	webhookRepository := postgres.NewWebhookRepository(dbConn)
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
