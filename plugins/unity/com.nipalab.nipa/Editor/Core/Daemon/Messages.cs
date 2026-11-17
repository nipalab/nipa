using System.Collections.Generic;

namespace Nipa.Editor.Core.Daemon
{
    public sealed class PingRequest { }

    public sealed class PingResponse
    {
        public string Version { get; set; }
        public int Pid { get; set; }
    }

    public sealed class WatchRepoRequest
    {
        public string Root { get; set; }
    }

    public sealed class RepoInfo
    {
        public string Root { get; set; }
        public string Url { get; set; }
        public string Branch { get; set; }
        public List<string> Sparse { get; set; }
    }

    public sealed class WatchRepoResponse
    {
        public RepoInfo Repo { get; set; }
    }

    public sealed class StatusRequest
    {
        public string Root { get; set; }
        public bool NoCache { get; set; }
    }

    public sealed class StatusHead
    {
        public string Kind { get; set; }
        public string Name { get; set; }
    }

    public sealed class StatusResponse
    {
        public List<string> Staged { get; set; }
        public List<string> Deleted { get; set; }
        public List<string> Modified { get; set; }
        public List<string> Untracked { get; set; }
        public List<string> Missing { get; set; }
        public List<string> Conflicts { get; set; }
        public string Branch { get; set; }
        public StatusHead Head { get; set; }
        public List<string> Added { get; set; }
        public string Pending { get; set; }
        public int PendingRemaining { get; set; }
        public List<string> Resolved { get; set; }
    }

    public sealed class StageRequest
    {
        public string Root { get; set; }
        public List<string> Add { get; set; }
        public List<string> Unstage { get; set; }
    }

    public sealed class RestoreRequest
    {
        public string Root { get; set; }
        public List<string> Paths { get; set; }
    }

    public sealed class UpdateRequest
    {
        public string Root { get; set; }
    }

    public sealed class SwitchRequest
    {
        public string Root { get; set; }
        public string Branch { get; set; }
        public string Tag { get; set; }
    }

    public sealed class PushRequest
    {
        public string Root { get; set; }
        public string Message { get; set; }
    }

    public sealed class WatchChangesRequest
    {
        public string Root { get; set; }
    }

    public sealed class RepoChange
    {
        public List<string> Paths { get; set; }
    }

    public sealed class MetaWarningsRequest
    {
        public string Root { get; set; }
    }

    public sealed class MetaWarning
    {
        public string Path { get; set; }
        public string Kind { get; set; }
        public string Related { get; set; }
        public string Message { get; set; }
    }

    public sealed class MetaWarningsResponse
    {
        public List<MetaWarning> Warnings { get; set; }
    }

    public sealed class OpQueued
    {
        public int Ahead { get; set; }
    }

    public sealed class OpStarted
    {
        public string Phase { get; set; }
    }

    public sealed class OpProgress
    {
        public string Phase { get; set; }
        public long ObjectsDone { get; set; }
        public long ObjectsTotal { get; set; }
        public long BytesDone { get; set; }
        public long BytesTotal { get; set; }
    }

    public sealed class OpFailure
    {
        public int Code { get; set; }
        public string Message { get; set; }
    }

    public sealed class SyncResult
    {
        public string Branch { get; set; }
        public string CommitId { get; set; }
    }

    public sealed class PushResult
    {
        public string CommitId { get; set; }
        public string CommitHash { get; set; }
        public string TreeHash { get; set; }
    }

    public sealed class MergeResult
    {
        public bool UpToDate { get; set; }
        public bool FastForwarded { get; set; }
        public bool MergeCommitted { get; set; }
        public bool Aborted { get; set; }
        public List<string> Conflicts { get; set; }
    }

    public sealed class ShelveResult
    {
        public Shelf Shelf { get; set; }
    }

    public sealed class ShelveApplyResult
    {
        public string ShelfId { get; set; }
        public string Name { get; set; }
        public List<string> Conflicts { get; set; }
        public bool Dropped { get; set; }
        public bool Aborted { get; set; }
    }

    public sealed class OpResult
    {
        public SyncResult Sync { get; set; }
        public PushResult Push { get; set; }
        public MergeResult Merge { get; set; }
        public ShelveResult Shelve { get; set; }
        public ShelveApplyResult ShelveApply { get; set; }
    }

    public sealed class OpEvent
    {
        public OpQueued Queued { get; set; }
        public OpStarted Started { get; set; }
        public OpProgress Progress { get; set; }
        public OpResult Result { get; set; }
        public OpFailure Failure { get; set; }
    }

    public sealed class FileLockDetail
    {
        public string Id { get; set; }
        public string Path { get; set; }
        public string Branch { get; set; }
        public bool Global { get; set; }
        public string HeldBy { get; set; }
        public string HeldByName { get; set; }
        public List<FileLockRequestDetail> Requests { get; set; }
    }

    public sealed class FileLockRequestDetail
    {
        public string Id { get; set; }
        public string RequesterId { get; set; }
        public string RequesterName { get; set; }
        public string Message { get; set; }
    }

    public sealed class RequestFileLockReleaseRequest
    {
        public string LockId { get; set; }
        public string Message { get; set; }
    }

    public sealed class ProxyLockRequestReleaseRequest
    {
        public string Root { get; set; }
        public RequestFileLockReleaseRequest Request { get; set; }
    }

    public sealed class ProxyLockRequestReleaseResponse
    {
    }

    public sealed class FileLockState
    {
        public string Path { get; set; }
        public string State { get; set; }
        public string LockPath { get; set; }
        public string HeldBy { get; set; }
        public string HeldByName { get; set; }
        public bool Tracked { get; set; }
    }

    public sealed class LockStatusRequest
    {
        public string Root { get; set; }
    }

    public sealed class LockStatusResponse
    {
        public string UserId { get; set; }
        public string Branch { get; set; }
        public bool DefaultBranch { get; set; }
        public bool CanReleaseAny { get; set; }
        public List<FileLockDetail> Locks { get; set; }
        public List<FileLockState> Files { get; set; }
    }

    public sealed class LockFileRequest
    {
        public string Path { get; set; }
        public string Branch { get; set; }
    }

    public sealed class ProxyLockFileRequest
    {
        public string Root { get; set; }
        public LockFileRequest Request { get; set; }
    }

    public sealed class ProxyLockFileResponse
    {
    }

    public sealed class UnlockFileRequest
    {
        public string Path { get; set; }
        public string Branch { get; set; }
    }

    public sealed class ProxyUnlockFileRequest
    {
        public string Root { get; set; }
        public UnlockFileRequest Request { get; set; }
    }

    public sealed class ProxyUnlockFileResponse
    {
    }

    public sealed class WatchEventsRequest
    {
        public string Root { get; set; }
        public int IntervalSeconds { get; set; }
    }

    public sealed class TeamEvent
    {
        public string Kind { get; set; }
        public string Actor { get; set; }
        public string Branch { get; set; }
        public string CommitId { get; set; }
        public string Message { get; set; }
        public int Count { get; set; }
        public string Path { get; set; }
        public long Number { get; set; }
    }

    public sealed class RepoActivity
    {
        public List<TeamEvent> Events { get; set; }
        public int Incoming { get; set; }
        public int ReviewRequests { get; set; }
        public string Error { get; set; }
    }

    public sealed class ConflictsRequest
    {
        public string Root { get; set; }
    }

    public sealed class ConflictRevision
    {
        public string Id { get; set; }
        public string Label { get; set; }
    }

    public sealed class ConflictFile
    {
        public string Path { get; set; }
        public string Kind { get; set; }
        public bool Binary { get; set; }
        public bool Resolved { get; set; }
        public string Viewer { get; set; }
    }

    public sealed class ConflictsResponse
    {
        public string Op { get; set; }
        public ConflictRevision Base { get; set; }
        public ConflictRevision Ours { get; set; }
        public ConflictRevision Theirs { get; set; }
        public List<ConflictFile> Files { get; set; }
    }

    public sealed class ResolveConflictRequest
    {
        public string Root { get; set; }
        public string Path { get; set; }
        public string Choice { get; set; }
        public byte[] Content { get; set; }
    }

    public sealed class BlobRef
    {
        public string Source { get; set; }
        public string Revision { get; set; }
        public string Path { get; set; }
    }

    public sealed class ReadBlobRequest
    {
        public string Root { get; set; }
        public BlobRef Blob { get; set; }
    }

    public sealed class BlobInfo
    {
        public string Path { get; set; }
        public long SizeBytes { get; set; }
        public bool IsBinary { get; set; }
    }

    public sealed class BlobEvent
    {
        public BlobInfo Info { get; set; }
        public byte[] Data { get; set; }
        public OpFailure Failure { get; set; }
    }

    public sealed class RevertOpRequest
    {
        public string Root { get; set; }
        public bool Abort { get; set; }
        public bool Continue { get; set; }
        public bool Skip { get; set; }
    }

    public sealed class MergeOpRequest
    {
        public string Root { get; set; }
        public string SourceBranch { get; set; }
        public bool Abort { get; set; }
        public string Message { get; set; }
    }

    public sealed class CreateBranchRequest
    {
        public string Name { get; set; }
        public string FromBranch { get; set; }
        public string FromCommitId { get; set; }
    }

    public sealed class CreateBranchResponse
    {
        public Branch Branch { get; set; }
    }

    public sealed class ProxyBranchCreateRequest
    {
        public string Root { get; set; }
        public CreateBranchRequest Request { get; set; }
    }

    public sealed class ProxyBranchCreateResponse
    {
        public CreateBranchResponse Response { get; set; }
    }

    public sealed class DeleteBranchRequest
    {
        public string Name { get; set; }
    }

    public sealed class ProxyBranchDeleteRequest
    {
        public string Root { get; set; }
        public DeleteBranchRequest Request { get; set; }
    }

    public sealed class ProxyBranchDeleteResponse
    {
    }

    public sealed class SyncPlanRequest
    {
        public string Root { get; set; }
        public string Branch { get; set; }
        public string Tag { get; set; }
    }

    public sealed class SyncPlanResponse
    {
        public List<string> Overwritten { get; set; }
    }

    public sealed class ShelfEntry
    {
        public string Path { get; set; }
        public string Status { get; set; }
        public bool Staged { get; set; }
        public bool Untracked { get; set; }
        public bool Locked { get; set; }
        public long SizeBytes { get; set; }
        public bool IsBinary { get; set; }
    }

    public sealed class Shelf
    {
        public string Id { get; set; }
        public string Name { get; set; }
        public long CreatedAt { get; set; }
        public string Branch { get; set; }
        public string Tag { get; set; }
        public string BaseCommitId { get; set; }
        public List<ShelfEntry> Entries { get; set; }
        public long SizeBytes { get; set; }
    }

    public sealed class ShelveCreateRequest
    {
        public string Root { get; set; }
        public string Name { get; set; }
        public List<string> Paths { get; set; }
        public bool IncludeUntracked { get; set; }
    }

    public sealed class ShelveApplyRequest
    {
        public string Root { get; set; }
        public string Id { get; set; }
        public bool Keep { get; set; }
        public bool Abort { get; set; }
    }

    public sealed class ShelveListRequest
    {
        public string Root { get; set; }
    }

    public sealed class ShelveListResponse
    {
        public List<Shelf> Shelves { get; set; }
        public string Applying { get; set; }
    }

    public sealed class ShelveDropRequest
    {
        public string Root { get; set; }
        public string Id { get; set; }
    }

    public sealed class ShelveDropResponse
    {
    }

    public sealed class Branch
    {
        public string Id { get; set; }
        public string Name { get; set; }
        public bool IsDefault { get; set; }
        public bool IsProtected { get; set; }
        public string UpdatedAt { get; set; }
    }

    public sealed class ListBranchesRequest
    {
        public int Limit { get; set; }
        public string LastUpdatedAt { get; set; }
        public string LastId { get; set; }
    }

    public sealed class ListBranchesResponse
    {
        public List<Branch> Branches { get; set; }
    }

    public sealed class ProxyBranchListRequest
    {
        public string Root { get; set; }
        public ListBranchesRequest Request { get; set; }
    }

    public sealed class ProxyBranchListResponse
    {
        public ListBranchesResponse Response { get; set; }
    }

    public sealed class Tag
    {
        public string Id { get; set; }
        public string Name { get; set; }
        public string CommitId { get; set; }
        public string Message { get; set; }
        public string CreatedAt { get; set; }
    }

    public sealed class ListTagsRequest
    {
        public int Limit { get; set; }
        public string LastCreatedAt { get; set; }
        public string LastId { get; set; }
    }

    public sealed class ListTagsResponse
    {
        public List<Tag> Tags { get; set; }
    }

    public sealed class ProxyTagListRequest
    {
        public string Root { get; set; }
        public ListTagsRequest Request { get; set; }
    }

    public sealed class ProxyTagListResponse
    {
        public ListTagsResponse Response { get; set; }
    }

    public sealed class CommitLogRequest
    {
        public string Branch { get; set; }
        public string StartCommitId { get; set; }
        public int Limit { get; set; }
        public string Path { get; set; }
    }

    public sealed class CommitLogEntry
    {
        public string CommitId { get; set; }
        public string CommitHash { get; set; }
        public string AuthorName { get; set; }
        public string Message { get; set; }
        public string CreatedAt { get; set; }
    }

    public sealed class CommitLogResponse
    {
        public string Branch { get; set; }
        public List<CommitLogEntry> Commits { get; set; }
    }

    public sealed class ProxyCommitLogRequest
    {
        public string Root { get; set; }
        public CommitLogRequest Request { get; set; }
    }

    public sealed class ProxyCommitLogResponse
    {
        public CommitLogResponse Response { get; set; }
    }
}
