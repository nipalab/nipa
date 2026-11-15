package server

import "github.com/nipalab/nipa/internal/maintenance"

const nipaSvc = "/greet.NipaService/"

// methodClass classifies every gRPC method for maintenance. A method missing
// here is treated as a write; TestMethodClassesCoverServiceDesc keeps it
// exhaustive.
var methodClass = buildMethodClasses(map[maintenance.Class][]string{
	maintenance.ClassPublic: {
		nipaSvc + "GetServerVersion",
		nipaSvc + "GetMaintenanceStatus",
		"/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch",
		"/grpc.health.v1.Health/List",
	},
	maintenance.ClassAuth: {
		nipaSvc + "LoginWithUsernamePassword",
		nipaSvc + "LoginWithRefreshToken",
		nipaSvc + "BeginLoginSSHKey",
		nipaSvc + "CompleteLoginSSHKey",
		nipaSvc + "CompleteLoginMFA",
	},
	maintenance.ClassRead: {
		nipaSvc + "ListMyProjects",
		nipaSvc + "ListDirectory",
		nipaSvc + "ListProjectMembers",
		nipaSvc + "GetListBranch",
		nipaSvc + "GetBranch",
		nipaSvc + "GetBranchByName",
		nipaSvc + "GetDefaultBranch",
		nipaSvc + "ListTags",
		nipaSvc + "GetTagByName",
		nipaSvc + "GetTreeManifest",
		nipaSvc + "StreamTreeManifest",
		nipaSvc + "GetCommitLog",
		nipaSvc + "GetCommit",
		nipaSvc + "WalkCommits",
		nipaSvc + "GetMergeBase",
		nipaSvc + "ListMergeRequests",
		nipaSvc + "GetMergeRequest",
		nipaSvc + "CheckMergeRequest",
		nipaSvc + "ListMergeRequestChecks",
		nipaSvc + "ListMergeRequestCommits",
		nipaSvc + "GetMergeRequestDiff",
		nipaSvc + "StreamMergeRequestDiff",
		nipaSvc + "ListMergeRequestReviews",
		nipaSvc + "GetMergeRequestReviewState",
		nipaSvc + "ListMergeRequestThreads",
		nipaSvc + "ListMergeRequestReviewRequests",
		nipaSvc + "ListMergeRequestTimeline",
		nipaSvc + "GetChunkDownloadUrls",
		nipaSvc + "ListFileLocks",
		nipaSvc + "GetMyPermissions",
		nipaSvc + "ListPBACRules",
		nipaSvc + "ListProjectPathPermissions",
		nipaSvc + "ListGroups",
	},
	maintenance.ClassWrite: {
		nipaSvc + "CreateBranch",
		nipaSvc + "RenameBranch",
		nipaSvc + "DeleteBranch",
		nipaSvc + "SetDefaultBranch",
		nipaSvc + "SetBranchProtection",
		nipaSvc + "CreateTag",
		nipaSvc + "DeleteTag",
		nipaSvc + "MergeFastForward",
		nipaSvc + "CreateMergeRequest",
		nipaSvc + "UpdateMergeRequest",
		nipaSvc + "SetMergeRequestAssignees",
		nipaSvc + "MergeMergeRequest",
		nipaSvc + "CloseMergeRequest",
		nipaSvc + "ReopenMergeRequest",
		nipaSvc + "ReportMergeRequestCheck",
		nipaSvc + "SubmitMergeRequestReview",
		nipaSvc + "WithdrawMergeRequestReview",
		nipaSvc + "DismissMergeRequestReview",
		nipaSvc + "AddMergeRequestComment",
		nipaSvc + "ReplyMergeRequestThread",
		nipaSvc + "UpdateMergeRequestComment",
		nipaSvc + "DeleteMergeRequestComment",
		nipaSvc + "ResolveMergeRequestThread",
		nipaSvc + "DeleteMergeRequestThread",
		nipaSvc + "RequestMergeRequestReview",
		nipaSvc + "RemoveMergeRequestReviewRequest",
		nipaSvc + "Push",
		nipaSvc + "PushStream",
		nipaSvc + "GetChunkUploadUrls",
		nipaSvc + "ConfirmChunkUploads",
		nipaSvc + "LockFile",
		nipaSvc + "UnlockFile",
		nipaSvc + "RequestFileLockRelease",
		nipaSvc + "CreatePBACRule",
		nipaSvc + "DeletePBACRule",
		nipaSvc + "SetProjectPathPermission",
		nipaSvc + "DeleteProjectPathPermission",
		nipaSvc + "CreateGroup",
		nipaSvc + "AddGroupMember",
		nipaSvc + "RemoveGroupMember",
	},
})

func buildMethodClasses(groups map[maintenance.Class][]string) map[string]maintenance.Class {
	out := make(map[string]maintenance.Class)
	for class, methods := range groups {
		for _, m := range methods {
			out[m] = class
		}
	}
	return out
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
