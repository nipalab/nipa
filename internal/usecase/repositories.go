package usecase

// Repositories is the persistence set both editions hand to the server
// wiring. Each field embeds every repository interface the usecases need from
// that store, so a concrete repository can be assigned directly.
type Repositories struct {
	Org interface {
		orgMemberRepository
		orgSlugLookup
		hookOrgLookup
	}
	OrgInvitation orgInvitationRepository
	Project       interface {
		projectRepository
		projectSlugLookup
		hookProjectLookup
	}
	Auth interface {
		authRepository
	}
	SSHKey interface {
		authSSHKeyRepository
		sshKeyRepository
	}
	User interface {
		userRepository
		userLookup
		userAdminRepository
	}
	Branch interface {
		branchRepository
		hookDefaultBranchLookup
	}
	Push interface {
		pushRepository
		chunkRepository
		mergeCommitter
	}
	PBAC  pbacRepository
	Group interface {
		groupRepository
		groupLookup
	}
	FileLock           fileLockRepository
	FileLockSerializer lockSerializer
	MergeRequest       interface {
		mergeRequestRepository
		notifierMRRepository
	}
	MergeRequestReview interface {
		mergeRequestReviewRepository
		notifierReviewRepository
	}
	MergeRequestCheck mergeRequestCheckRepository
	Tag               tagRepository
	Webhook           interface {
		webhookRepository
		hookActiveRepository
	}
	Email interface {
		emailDeliveryRepository
		notifierOutbox
	}
	Audit        auditRepository
	ServiceToken serviceTokenRepository
}
