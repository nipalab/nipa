package domain

// Audit action catalog. Reads are never audited.
const (
	AuditOrgCreated           = "org.created"
	AuditOrgMemberAdded       = "org.member.added"
	AuditOrgMemberRoleChanged = "org.member.role_changed"
	AuditOrgMemberRemoved     = "org.member.removed"

	AuditOrgInvitationCreated  = "org.invitation.created"
	AuditOrgInvitationResent   = "org.invitation.resent"
	AuditOrgInvitationRevoked  = "org.invitation.revoked"
	AuditOrgInvitationAccepted = "org.invitation.accepted"
	AuditOrgInvitationDeclined = "org.invitation.declined"

	AuditGroupCreated       = "group.created"
	AuditGroupMemberAdded   = "group.member.added"
	AuditGroupMemberRemoved = "group.member.removed"

	AuditAccessRuleCreated           = "access.rule.created"
	AuditAccessRuleDeleted           = "access.rule.deleted"
	AuditAccessPathPermissionSet     = "access.path_permission.set"
	AuditAccessPathPermissionDeleted = "access.path_permission.deleted"

	AuditBranchProtectionChanged = "branch.protection.changed"

	AuditLockForceReleased = "lock.force_released"

	AuditUserCreated              = "user.created"
	AuditUserAdminFlagsChanged    = "user.admin_flags.changed"
	AuditUserDeactivated          = "user.deactivated"
	AuditUserPasswordResetByAdmin = "user.password.reset_by_admin"
	AuditUserEmailChanged         = "user.email.changed"
	AuditUserTermsAccepted        = "user.terms.accepted"

	AuditAuthRefreshTokenReuseDetected = "auth.refresh_token.reuse_detected"

	AuditProjectCreated = "project.created"
	AuditProjectUpdated = "project.updated"
	AuditProjectDeleted = "project.deleted"

	AuditWebhookCreated       = "webhook.created"
	AuditWebhookUpdated       = "webhook.updated"
	AuditWebhookDeleted       = "webhook.deleted"
	AuditWebhookSecretRotated = "webhook.secret_rotated"

	AuditServiceTokenCreated = "service_token.created"
	AuditServiceTokenRevoked = "service_token.revoked"

	AuditBillingQuotaOverrideSet     = "billing.quota_override.set"
	AuditBillingQuotaOverrideCleared = "billing.quota_override.cleared"
	AuditBillingOwnerTransferred     = "billing.owner.transferred"
	AuditBillingCheckoutStarted      = "billing.checkout.started"
	AuditBillingSubscriptionChanged  = "billing.subscription.changed"
	AuditBillingSubscriptionApplied  = "billing.subscription.applied"
	AuditBillingSync                 = "billing.sync"
	AuditBillingEventReplayed        = "billing.event.replayed"
	AuditBillingSeatDenied           = "billing.seat.denied"
	AuditBillingQuotasReconciled     = "billing.quotas.reconciled"
)

// Audit target types.
const (
	AuditTargetOrg            = "org"
	AuditTargetUser           = "user"
	AuditTargetInvitation     = "invitation"
	AuditTargetGroup          = "group"
	AuditTargetAccessRule     = "access_rule"
	AuditTargetPathPermission = "path_permission"
	AuditTargetBranch         = "branch"
	AuditTargetLock           = "lock"
	AuditTargetProject        = "project"
	AuditTargetWebhook        = "webhook"
	AuditTargetServiceToken   = "service_token"
	AuditTargetSubscription   = "subscription"
	AuditTargetBillingEvent   = "billing_event"
)
