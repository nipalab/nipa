package serverapp

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/totp"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/nipalab/nipa/internal/webhook"
)

// UsecaseDeps carries everything the usecase graph needs besides the
// repositories. StorageLedger, SeatGate, EmailDispatcher and RateLimiter are
// optional.
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
	SeatGate          usecase.SeatGate
	RateLimiter       *ratelimit.Limiter
}

// BuildUsecases wires the whole usecase graph once for every edition and for
// the in-process test harnesses.
func BuildUsecases(repos usecase.Repositories, deps UsecaseDeps) Usecases {
	cfg := deps.Config
	node := deps.Snow
	transactor := dbtx.NewTransactor(deps.DB)

	auditUsecase := usecase.NewAudit(repos.Audit, node, transactor).WithProjectOrgs(repos.PBAC)
	authUsecase := usecase.NewAuth(cfg.JWTKey, deps.Hasher, repos.User, repos.Auth).WithSSHKeys(repos.SSHKey).WithLimiter(deps.RateLimiter).WithSSHAudience(sshAudience(cfg)).WithAudit(auditUsecase)
	mfaUsecase := usecase.NewMFA(repos.MFA, repos.User, deps.Hasher, mfaCipher(cfg), transactor).WithAudit(auditUsecase).WithLimiter(deps.RateLimiter)
	if repos.MFA != nil {
		authUsecase = authUsecase.WithMFA(mfaUsecase)
		mfaUsecase.LogEnrolledWithoutKey(context.Background())
	}
	orgUsecase := usecase.NewOrg(repos.Org, node).WithAudit(auditUsecase)
	permissionUsecase := usecase.NewPermission(repos.PBAC, repos.User, repos.Group, orgUsecase).WithAudit(auditUsecase)
	orgUsecase.WithPermissionCache(permissionUsecase)
	groupUsecase := usecase.NewGroup(repos.Group, node, permissionUsecase, orgUsecase).WithAudit(auditUsecase)
	if deps.SeatGate != nil {
		orgUsecase.WithSeatGate(deps.SeatGate)
		groupUsecase.WithSeatGate(deps.SeatGate)
		permissionUsecase.WithSeatGate(deps.SeatGate)
	}
	orgInvitationUsecase := usecase.NewOrgInvitation(repos.OrgInvitation, orgUsecase, repos.User, node).WithLimiter(deps.RateLimiter)
	projectUsecase := usecase.NewProject(repos.Project, node, permissionUsecase, orgUsecase).WithAudit(auditUsecase)

	branchUsecase := usecase.NewBranchWithChunks(permissionUsecase, repos.Branch, node, deps.ChunkStore).WithUsers(repos.User).WithTags(repos.Tag).WithAudit(auditUsecase)
	fileLockUsecase := usecase.NewFileLock(repos.FileLock, repos.Branch, permissionUsecase, node).WithAudit(auditUsecase)
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

	commonUsecase := usecase.NewCommon(repos.Org, repos.Project)
	serviceTokenUsecase := usecase.NewServiceToken(repos.ServiceToken, permissionUsecase, repos.User, commonUsecase, node).WithAudit(auditUsecase)
	webhookUsecase := usecase.NewWebhook(repos.Webhook, permissionUsecase, repos.User, deps.WebhookDispatcher, node).WithAudit(auditUsecase)
	hookEmitter := usecase.NewHookEmitter(repos.Webhook, repos.Project, repos.Org, repos.Branch, repos.User, deps.WebhookDispatcher)
	emailDeliveryUsecase := usecase.NewEmailDelivery(repos.Email, permissionUsecase)
	if deps.EmailDispatcher != nil {
		emailDeliveryUsecase = emailDeliveryUsecase.WithKicker(deps.EmailDispatcher)
		hookEmitter = hookEmitter.WithNotifier(usecase.NewEmailNotifier(
			repos.Email, repos.User, repos.MergeRequest, repos.MergeRequestReview, cfg.EmailBaseURL, node,
		).WithKicker(deps.EmailDispatcher))
		orgInvitationUsecase = orgInvitationUsecase.WithOutbox(repos.Email, cfg.EmailBaseURL).WithKicker(deps.EmailDispatcher)
		mfaUsecase = mfaUsecase.WithNotifier(usecase.NewMFAMailer(repos.Email, node, cfg.EmailBaseURL).WithKicker(deps.EmailDispatcher))
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
		User:               usecase.NewUser(node, repos.User, deps.Hasher).WithAudit(auditUsecase).WithLimiter(deps.RateLimiter),
		SSHKey:             usecase.NewSSHKey(node, repos.SSHKey),
		MFA:                mfaUsecase,
		Common:             commonUsecase,
		Branch:             branchUsecase,
		Tag:                tagUsecase,
		Push:               pushUsecase,
		Chunk:              chunkUsecase,
		Permission:         permissionUsecase,
		Group:              groupUsecase,
		Org:                orgUsecase,
		OrgInvitation:      orgInvitationUsecase,
		Project:            projectUsecase,
		MergeRequest:       mergeRequestUsecase,
		MergeRequestReview: reviewUsecase,
		MergeRequestCheck:  checkUsecase,
		FileLock:           fileLockUsecase,
		Webhook:            webhookUsecase,
		ServiceToken:       serviceTokenUsecase,
		EmailDelivery:      emailDeliveryUsecase,
		Audit:              auditUsecase,
	}
}

// mfaCipher builds the TOTP secret cipher from MFA_ENCRYPTION_KEY; without a
// usable key two-factor authentication stays unavailable.
func mfaCipher(cfg *config.Config) *totp.Cipher {
	key, err := cfg.MFAKey()
	if err != nil {
		slog.Error("two-factor authentication disabled", "error", err)
		return nil
	}
	if key == nil {
		slog.Warn("two-factor authentication disabled: set MFA_ENCRYPTION_KEY (openssl rand -base64 32)")
		return nil
	}
	cipher, err := totp.NewCipher(key)
	if err != nil {
		slog.Error("two-factor authentication disabled", "error", err)
		return nil
	}
	return cipher
}

func sshAudience(cfg *config.Config) string {
	origin, err := cfg.SSHLoginAudience()
	switch {
	case err != nil:
		slog.Error("SSH key login disabled", "error", err)
	case origin == "":
		slog.Warn("SSH key login disabled: set PUBLIC_BASE_URL (or EMAIL_BASE_URL) to the public address clients use")
	default:
		slog.Info("SSH key login bound to origin", "origin", origin)
	}
	return origin
}
