package domain

// Audit action catalog. Reads are never audited.
const (
	AuditOrgCreated           = "org.created"
	AuditOrgMemberAdded       = "org.member.added"
	AuditOrgMemberRoleChanged = "org.member.role_changed"
	AuditOrgMemberRemoved     = "org.member.removed"

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

	AuditProjectCreated = "project.created"
	AuditProjectUpdated = "project.updated"
	AuditProjectDeleted = "project.deleted"

	AuditWebhookCreated       = "webhook.created"
	AuditWebhookUpdated       = "webhook.updated"
	AuditWebhookDeleted       = "webhook.deleted"
	AuditWebhookSecretRotated = "webhook.secret_rotated"
)

// Audit target types.
const (
	AuditTargetOrg            = "org"
	AuditTargetUser           = "user"
	AuditTargetGroup          = "group"
	AuditTargetAccessRule     = "access_rule"
	AuditTargetPathPermission = "path_permission"
	AuditTargetBranch         = "branch"
	AuditTargetLock           = "lock"
	AuditTargetProject        = "project"
	AuditTargetWebhook        = "webhook"
)
