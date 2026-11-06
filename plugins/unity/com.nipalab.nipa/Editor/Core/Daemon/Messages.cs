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

    public sealed class OpResult
    {
        public SyncResult Sync { get; set; }
        public PushResult Push { get; set; }
    }

    public sealed class OpEvent
    {
        public OpQueued Queued { get; set; }
        public OpStarted Started { get; set; }
        public OpProgress Progress { get; set; }
        public OpResult Result { get; set; }
        public OpFailure Failure { get; set; }
    }
}
