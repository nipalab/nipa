package serverapp

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/webhook"
)

// UsecaseDeps carries everything the usecase graph needs besides the
// repositories. StorageLedger and EmailDispatcher are optional.
type UsecaseDeps struct {
	Config            *config.Config
	DB                *sql.DB
	Snow              snow.Node
	Hasher            *hasher.Hasher
	ChunkStore        storage.ChunkStore
	Metrics           *obs.Metrics
	WebhookDispatcher *webhook.Dispatcher
	EmailDispatcher   *mail.Dispatcher
	StorageLedger     usecase.StorageLedger
}

// BuildUsecases wires the whole usecase graph once for every edition and for
// the in-process test harnesses.
func BuildUsecases(repos usecase.Repositories, deps UsecaseDeps) Usecases {
	cfg := deps.Config
	node := deps.Snow
	transactor := dbtx.NewTransactor(deps.DB)

	authUsecase := usecase.NewAuth(cfg.JWTKey, deps.Hasher, repos.User, repos.Auth).WithSSHKeys(repos.SSHKey)
	orgUsecase := usecase.NewOrg(repos.Org, node)
	permissionUsecase := usecase.NewPermission(repos.PBAC, repos.User, repos.Group, orgUsecase)
	orgUsecase.WithPermissionCache(permissionUsecase)
	projectUsecase := usecase.NewProject(repos.Project, node, permissionUsecase, orgUsecase)

	branchUsecase := usecase.NewBranchWithChunks(permissionUsecase, repos.Branch, node, deps.ChunkStore).WithUsers(repos.User)
	fileLockUsecase := usecase.NewFileLock(repos.FileLock, repos.Branch, permissionUsecase, node)
	if repos.FileLockSerializer != nil {
		fileLockUsecase = fileLockUsecase.WithSerializer(transactor, repos.FileLockSerializer)
	}
	branchUsecase = branchUsecase.WithFileLocks(fileLockUsecase)

	mergeRequestUsecase := usecase.NewMergeRequest(repos.MergeRequest, repos.Branch, permissionUsecase, branchUsecase, node, transactor).
		WithFileLocks(fileLockUsecase)
	pushUsecase := usecase.NewPush(permissionUsecase, repos.Branch, repos.Push, node).
		WithFileLocks(fileLockUsecase).WithMetrics(deps.Metrics)
	tagUsecase := usecase.NewTag(permissionUsecase, repos.Tag, repos.Branch, node)
	reviewUsecase := usecase.NewMergeRequestReview(
		repos.MergeRequestReview, repos.MergeRequest, repos.Branch, branchUsecase, permissionUsecase, repos.User, node,
	).WithAccessChecker(permissionUsecase)
	pushUsecase = pushUsecase.WithReviews(reviewUsecase)
	mergeRequestUsecase = mergeRequestUsecase.
		WithReview(reviewUsecase).
		WithUsers(repos.User).
		WithAccessChecker(permissionUsecase)

	chunkUsecase := usecase.NewChunk(repos.Push, deps.ChunkStore, usecase.ChunkTransferConfig{
		SigningKey:  cfg.ChunkURLSigningKey,
		PresignTTL:  time.Duration(cfg.ChunkPresignTTLSeconds) * time.Second,
		MaxPageSize: cfg.ChunkMaxPageSize,
	}).WithMetrics(deps.Metrics)
	if deps.StorageLedger != nil {
		chunkUsecase = chunkUsecase.WithStorageLedger(deps.StorageLedger)
	}
	branchUsecase = branchUsecase.WithMergeCommitter(repos.Push).WithChunkUploader(chunkUsecase)

	webhookUsecase := usecase.NewWebhook(repos.Webhook, permissionUsecase, repos.User, deps.WebhookDispatcher, node)
	hookEmitter := usecase.NewHookEmitter(repos.Webhook, repos.Project, repos.Org, repos.Branch, repos.User, deps.WebhookDispatcher)
	emailDeliveryUsecase := usecase.NewEmailDelivery(repos.Email, permissionUsecase)
	if deps.EmailDispatcher != nil {
		emailDeliveryUsecase = emailDeliveryUsecase.WithKicker(deps.EmailDispatcher)
		hookEmitter = hookEmitter.WithNotifier(usecase.NewEmailNotifier(
			repos.Email, repos.User, repos.MergeRequest, repos.MergeRequestReview, cfg.EmailBaseURL, node,
		).WithKicker(deps.EmailDispatcher))
		slog.Info("email notifications enabled", "sender", cfg.EmailSender)
	}

	branchUsecase = branchUsecase.WithHooks(hookEmitter)
	tagUsecase = tagUsecase.WithHooks(hookEmitter)
	mergeRequestUsecase = mergeRequestUsecase.WithHooks(hookEmitter)
	reviewUsecase = reviewUsecase.WithHooks(hookEmitter)
	pushUsecase = pushUsecase.WithHooks(hookEmitter)
	checkUsecase := usecase.NewMergeRequestCheck(repos.MergeRequestCheck, repos.MergeRequest, repos.Branch, permissionUsecase, node).
		WithHooks(hookEmitter)
	mergeRequestUsecase = mergeRequestUsecase.WithChecks(checkUsecase)

	return Usecases{
		Auth:               authUsecase,
		User:               usecase.NewUser(node, repos.User, deps.Hasher),
		SSHKey:             usecase.NewSSHKey(node, repos.SSHKey),
		Common:             usecase.NewCommon(repos.Org, repos.Project),
		Branch:             branchUsecase,
		Tag:                tagUsecase,
		Push:               pushUsecase,
		Chunk:              chunkUsecase,
		Permission:         permissionUsecase,
		Group:              usecase.NewGroup(repos.Group, node, permissionUsecase, orgUsecase),
		Org:                orgUsecase,
		Project:            projectUsecase,
		MergeRequest:       mergeRequestUsecase,
		MergeRequestReview: reviewUsecase,
		MergeRequestCheck:  checkUsecase,
		FileLock:           fileLockUsecase,
		Webhook:            webhookUsecase,
		EmailDelivery:      emailDeliveryUsecase,
	}
}
