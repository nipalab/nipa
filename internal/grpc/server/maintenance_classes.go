package server

import "github.com/nipalab/nipa/internal/maintenance"

const (
	public  = maintenance.ClassPublic
	auth    = maintenance.ClassAuth
	read    = maintenance.ClassRead
	write   = maintenance.ClassWrite
	nipaSvc = "/greet.NipaService/"
)

// methodClass classifies every gRPC method for maintenance. A method missing
// here is treated as a write; TestMethodClassesCoverServiceDesc keeps it
// exhaustive.
var methodClass = map[string]maintenance.Class{
	nipaSvc + "LoginWithUsernamePassword": auth,
	nipaSvc + "LoginWithRefreshToken":     auth,
	nipaSvc + "BeginLoginSSHKey":          auth,
	nipaSvc + "CompleteLoginSSHKey":       auth,
	nipaSvc + "CompleteLoginMFA":          auth,
	nipaSvc + "GetServerVersion":          public,
	nipaSvc + "GetMaintenanceStatus":      public,

	nipaSvc + "ListMyProjects":     read,
	nipaSvc + "ListDirectory":      read,
	nipaSvc + "ListProjectMembers": read,

	nipaSvc + "GetListBranch":       read,
	nipaSvc + "GetBranch":           read,
	nipaSvc + "GetBranchByName":     read,
	nipaSvc + "GetDefaultBranch":    read,
	nipaSvc + "CreateBranch":        write,
	nipaSvc + "RenameBranch":        write,
	nipaSvc + "DeleteBranch":        write,
	nipaSvc + "SetDefaultBranch":    write,
	nipaSvc + "SetBranchProtection": write,

	nipaSvc + "ListTags":     read,
	nipaSvc + "GetTagByName": read,
	nipaSvc + "CreateTag":    write,
	nipaSvc + "DeleteTag":    write,

	nipaSvc + "GetTreeManifest":  read,
	nipaSvc + "GetCommitLog":     read,
	nipaSvc + "GetCommit":        read,
	nipaSvc + "WalkCommits":      read,
	nipaSvc + "GetMergeBase":     read,
	nipaSvc + "MergeFastForward": write,

	nipaSvc + "CreateMergeRequest":       write,
	nipaSvc + "UpdateMergeRequest":       write,
	nipaSvc + "ListMergeRequests":        read,
	nipaSvc + "GetMergeRequest":          read,
	nipaSvc + "SetMergeRequestAssignees": write,
	nipaSvc + "MergeMergeRequest":        write,
	nipaSvc + "CloseMergeRequest":        write,
	nipaSvc + "ReopenMergeRequest":       write,
	nipaSvc + "CheckMergeRequest":        read,
	nipaSvc + "ReportMergeRequestCheck":  write,
	nipaSvc + "ListMergeRequestChecks":   read,
	nipaSvc + "ListMergeRequestCommits":  read,
	nipaSvc + "GetMergeRequestDiff":      read,

	nipaSvc + "SubmitMergeRequestReview":        write,
	nipaSvc + "ListMergeRequestReviews":         read,
	nipaSvc + "GetMergeRequestReviewState":      read,
	nipaSvc + "WithdrawMergeRequestReview":      write,
	nipaSvc + "DismissMergeRequestReview":       write,
	nipaSvc + "ListMergeRequestThreads":         read,
	nipaSvc + "AddMergeRequestComment":          write,
	nipaSvc + "ReplyMergeRequestThread":         write,
	nipaSvc + "UpdateMergeRequestComment":       write,
	nipaSvc + "DeleteMergeRequestComment":       write,
	nipaSvc + "ResolveMergeRequestThread":       write,
	nipaSvc + "DeleteMergeRequestThread":        write,
	nipaSvc + "ListMergeRequestReviewRequests":  read,
	nipaSvc + "RequestMergeRequestReview":       write,
	nipaSvc + "RemoveMergeRequestReviewRequest": write,
	nipaSvc + "ListMergeRequestTimeline":        read,

	nipaSvc + "Push":                 write,
	nipaSvc + "GetChunkUploadUrls":   write,
	nipaSvc + "GetChunkDownloadUrls": read,
	nipaSvc + "ConfirmChunkUploads":  write,

	nipaSvc + "LockFile":               write,
	nipaSvc + "UnlockFile":             write,
	nipaSvc + "ListFileLocks":          read,
	nipaSvc + "RequestFileLockRelease": write,

	nipaSvc + "GetMyPermissions":            read,
	nipaSvc + "CreatePBACRule":              write,
	nipaSvc + "ListPBACRules":               read,
	nipaSvc + "DeletePBACRule":              write,
	nipaSvc + "ListProjectPathPermissions":  read,
	nipaSvc + "SetProjectPathPermission":    write,
	nipaSvc + "DeleteProjectPathPermission": write,

	nipaSvc + "CreateGroup":       write,
	nipaSvc + "ListGroups":        read,
	nipaSvc + "AddGroupMember":    write,
	nipaSvc + "RemoveGroupMember": write,

	"/grpc.health.v1.Health/Check": public,
	"/grpc.health.v1.Health/Watch": public,
	"/grpc.health.v1.Health/List":  public,
}

// serverScopeOnly lists the read and write methods whose request names no
// org or project, so only the server window covers them.
var serverScopeOnly = map[string]bool{
	nipaSvc + "ListMyProjects": true,
}

func classOf(method string) maintenance.Class {
	if c, ok := methodClass[method]; ok {
		return c
	}
	return maintenance.ClassWrite
}
