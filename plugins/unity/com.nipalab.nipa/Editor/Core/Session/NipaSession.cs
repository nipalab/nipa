using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Merge;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Status;

namespace Nipa.Editor.Core.Session
{
    public sealed class LockFailure
    {
        public string Path { get; }
        public string Message { get; }
        public string Holder { get; }

        public LockFailure(string path, string message, string holder = null)
        {
            Path = path;
            Message = message;
            Holder = holder;
        }
    }

    public sealed class LockResult
    {
        public List<string> Locked { get; } = new List<string>();
        public List<LockFailure> Failed { get; } = new List<LockFailure>();
    }

    public sealed class NipaSession : IDisposable
    {
        readonly Func<CancellationToken, Task<DaemonClient>> reconnect;
        readonly object gate = new object();
        Lease lease;
        readonly CancellationTokenSource cts = new CancellationTokenSource();
        Task watcher;
        Task poller;
        Task activity;
        LockStatusResponse lastLocks;
        volatile LockPolicy locks = LockPolicy.Empty;
        NipaSettings settings = new NipaSettings();

        public ProjectPaths Paths { get; }
        public StatusCache Cache { get; } = new StatusCache();
        public string Root => Paths.CloneRoot;
        public RepoInfo Repo { get; private set; }
        public TimeSpan ReconnectDelay { get; set; } = TimeSpan.FromSeconds(2);
        public TimeSpan PollInterval { get; set; } = TimeSpan.Zero;

        public LockPolicy Locks => locks;
        public int Incoming { get; private set; }
        public int ReviewRequests { get; private set; }

        public event Action<Exception> WatchFailed;
        public event Action Healthy;
        public event Action LocksChanged;
        public event Action<RepoActivity> Activity;

        public NipaSession(DaemonClient client, ProjectPaths paths,
            Func<CancellationToken, Task<DaemonClient>> reconnect = null)
        {
            lease = new Lease { Client = client };
            this.reconnect = reconnect;
            Paths = paths;
        }

        public async Task StartAsync(CancellationToken ct = default)
        {
            await WatchRepoAsync(ct).ConfigureAwait(false);
            watcher = Task.Run(() => WatchLoopAsync(cts.Token));
            activity = Task.Run(() => ActivityLoopAsync(cts.Token));
            if (PollInterval > TimeSpan.Zero)
                poller = Task.Run(() => PollLoopAsync(cts.Token));
        }

        async Task WatchRepoAsync(CancellationToken ct)
        {
            var watched = await Call<WatchRepoRequest, WatchRepoResponse>(
                "WatchRepo", new WatchRepoRequest { Root = Root }, ct).ConfigureAwait(false);
            Repo = watched.Repo;
            await RefreshAsync(ct).ConfigureAwait(false);
            await TryRefreshLocksAsync(ct).ConfigureAwait(false);
        }

        public void ApplySettings(NipaSettings next)
        {
            settings = next ?? new NipaSettings();
            LockStatusResponse last;
            lock (gate)
                last = lastLocks;
            if (last != null)
                SetLocks(last);
        }

        void SetLocks(LockStatusResponse status)
        {
            lock (gate)
                lastLocks = status;
            locks = new LockPolicy(status, settings.LockStrict, settings.LockExtensions);
            LocksChanged?.Invoke();
        }

        public async Task RefreshLocksAsync(CancellationToken ct = default)
        {
            var status = await Call<LockStatusRequest, LockStatusResponse>(
                "LockStatus", new LockStatusRequest { Root = Root }, ct).ConfigureAwait(false);
            SetLocks(status);
        }

        async Task TryRefreshLocksAsync(CancellationToken ct)
        {
            try
            {
                await RefreshLocksAsync(ct).ConfigureAwait(false);
            }
            catch (Exception e) when (!(e is OperationCanceledException))
            {
                WatchFailed?.Invoke(e);
            }
        }

        public async Task<LockResult> LockAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var result = new LockResult();
            try
            {
                foreach (var asset in Assets(clonePaths))
                {
                    if (locks.Lookup(asset).State == LockState.Theirs)
                    {
                        result.Failed.Add(new LockFailure(asset, "locked by someone else"));
                        continue;
                    }
                    var failed = false;
                    foreach (var p in ProjectPaths.Pair(asset))
                    {
                        if (locks.HeldByMe(p))
                            continue;
                        try
                        {
                            await Call<ProxyLockFileRequest, ProxyLockFileResponse>("ProxyLockFile",
                                new ProxyLockFileRequest { Root = Root, Request = new LockFileRequest { Path = p, Branch = locks.LockBranch } },
                                ct).ConfigureAwait(false);
                        }
                        catch (DaemonException e)
                        {
                            result.Failed.Add(new LockFailure(asset, e.Message));
                            failed = true;
                            break;
                        }
                    }
                    if (!failed)
                        result.Locked.Add(asset);
                }
            }
            finally
            {
                await TryRefreshLocksAsync(CancellationToken.None).ConfigureAwait(false);
            }
            for (var i = 0; i < result.Failed.Count; i++)
            {
                var info = locks.Lookup(result.Failed[i].Path);
                if (info.State == LockState.Theirs)
                    result.Failed[i] = new LockFailure(result.Failed[i].Path, result.Failed[i].Message, info.Holder);
            }
            return result;
        }

        public async Task UnlockAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            try
            {
                foreach (var asset in Assets(clonePaths))
                {
                    foreach (var l in locks.Mine.Where(l => ProjectPaths.AssetOf(Trim(l.Path)) == asset).ToList())
                        await ReleaseOneAsync(l, ct).ConfigureAwait(false);
                }
            }
            finally
            {
                await TryRefreshLocksAsync(CancellationToken.None).ConfigureAwait(false);
            }
        }

        public async Task ReleaseAsync(FileLockDetail l, CancellationToken ct = default)
        {
            var asset = ProjectPaths.AssetOf(Trim(l.Path));
            var pair = locks.Locks.Where(x => locks.InScope(x) && locks.CanRelease(x) && ProjectPaths.AssetOf(Trim(x.Path)) == asset).ToList();
            if (!pair.Contains(l))
                pair.Add(l);
            try
            {
                foreach (var x in pair)
                    await ReleaseOneAsync(x, ct).ConfigureAwait(false);
            }
            finally
            {
                await TryRefreshLocksAsync(CancellationToken.None).ConfigureAwait(false);
            }
        }

        async Task ReleaseOneAsync(FileLockDetail l, CancellationToken ct)
        {
            try
            {
                await Call<ProxyUnlockFileRequest, ProxyUnlockFileResponse>("ProxyUnlockFile",
                    new ProxyUnlockFileRequest { Root = Root, Request = new UnlockFileRequest { Path = l.Path, Branch = l.Global ? string.Empty : l.Branch } },
                    ct).ConfigureAwait(false);
            }
            catch (DaemonException e) when (e.Code == 404)
            {
            }
        }

        public async Task<PushCheck> CheckPushAsync(CancellationToken ct = default)
        {
            await RefreshLocksAsync(ct).ConfigureAwait(false);
            return locks.CheckPush();
        }

        public bool IsOpenForEdit(string clonePath, out string message)
        {
            var state = Cache.Get(ProjectPaths.AssetOf(clonePath));
            var isNew = (state & (FileState.Untracked | FileState.Added)) != 0;
            return locks.IsOpenForEdit(clonePath, isNew, out message);
        }

        public List<string> WritablePaths(IEnumerable<string> clonePaths, out List<string> rejected)
        {
            var allowed = new List<string>();
            rejected = new List<string>();
            foreach (var p in clonePaths)
            {
                if (IsOpenForEdit(p, out var message))
                    allowed.Add(p);
                else if (locks.Lookup(p).State == LockState.Theirs)
                    rejected.Add(message);
                else
                    allowed.Add(p);
            }
            return allowed;
        }

        static List<string> Assets(IEnumerable<string> clonePaths)
        {
            var seen = new HashSet<string>();
            var result = new List<string>();
            foreach (var raw in clonePaths)
            {
                var p = ProjectPaths.AssetOf(Trim(raw));
                if (p.Length > 0 && seen.Add(p))
                    result.Add(p);
            }
            return result;
        }

        static string Trim(string p)
        {
            return ProjectPaths.Normalize(p).Trim('/');
        }

        async Task ReconnectAsync(CancellationToken ct)
        {
            var fresh = await reconnect(ct).ConfigureAwait(false);
            Lease old;
            lock (gate)
            {
                old = lease;
                lease = new Lease { Client = fresh };
            }
            Retire(old);
            await WatchRepoAsync(ct).ConfigureAwait(false);
        }

        public List<string> Restorable(IEnumerable<string> clonePaths)
        {
            var result = new List<string>();
            var seen = new HashSet<string>();
            Dictionary<string, FileState> files = null;
            foreach (var raw in clonePaths)
            {
                var p = ProjectPaths.Normalize(raw).Trim('/');
                if (p.Length == 0)
                    continue;
                if (Cache.Has(p))
                {
                    if (IsRestorable(p) && seen.Add(p))
                        result.Add(p);
                    continue;
                }
                if (Cache.Get(p) == FileState.None)
                {
                    if (IsRestorable(p) && seen.Add(p))
                        result.Add(p);
                    continue;
                }
                var folderMeta = ProjectPaths.MetaOf(p);
                if (Cache.Has(folderMeta) && IsRestorable(folderMeta) && seen.Add(folderMeta))
                    result.Add(folderMeta);
                files = files ?? Cache.Snapshot().ToDictionary(kv => kv.Key, kv => kv.Value);
                var prefix = p + "/";
                foreach (var child in files.Keys.Where(k => k.StartsWith(prefix, StringComparison.Ordinal)).OrderBy(k => k, StringComparer.Ordinal))
                {
                    if (IsRestorable(child) && seen.Add(child))
                        result.Add(child);
                }
            }
            return result;
        }

        bool IsRestorable(string path)
        {
            const FileState Changed = FileState.Modified | FileState.Deleted | FileState.Missing;
            const FileState Blocked = FileState.Staged | FileState.Added | FileState.Untracked;
            var own = Cache.Get(path);
            if ((own & Blocked) != 0)
                return false;
            return ((own | Cache.Get(ProjectPaths.MetaOf(path))) & Changed) != 0;
        }

        public async Task RefreshAsync(CancellationToken ct = default)
        {
            var status = await Call<StatusRequest, StatusResponse>(
                "Status", new StatusRequest { Root = Root }, ct).ConfigureAwait(false);
            Cache.Apply(status);
            Healthy?.Invoke();
        }

        public async Task StageAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var status = await Call<StageRequest, StatusResponse>(
                "Stage", new StageRequest { Root = Root, Add = Expand(clonePaths) }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public async Task UnstageAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var status = await Call<StageRequest, StatusResponse>(
                "Stage", new StageRequest { Root = Root, Unstage = Expand(clonePaths) }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public async Task RestoreAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var status = await Call<RestoreRequest, StatusResponse>(
                "Restore", new RestoreRequest { Root = Root, Paths = Expand(clonePaths) }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public Task<OpResult> PushAsync(string message, Action<OpEvent> onEvent, CancellationToken ct = default)
        {
            return RunOp("Push", new PushRequest { Root = Root, Message = message }, onEvent, ct);
        }

        public Task<OpResult> UpdateAsync(Action<OpEvent> onEvent, CancellationToken ct = default)
        {
            return RunOp("Update", new UpdateRequest { Root = Root }, onEvent, ct);
        }

        public Task<OpResult> SwitchAsync(string branch, string tag, Action<OpEvent> onEvent, CancellationToken ct = default)
        {
            return RunOp("Switch", new SwitchRequest { Root = Root, Branch = branch, Tag = tag }, onEvent, ct);
        }

        public async Task<List<Branch>> BranchesAsync(CancellationToken ct = default)
        {
            const int page = 200;
            const int maxPages = 50;
            var all = new List<Branch>();
            var request = new ListBranchesRequest { Limit = page };
            for (var i = 0; i < maxPages; i++)
            {
                var res = await Call<ProxyBranchListRequest, ProxyBranchListResponse>("ProxyBranchList",
                    new ProxyBranchListRequest { Root = Root, Request = request }, ct).ConfigureAwait(false);
                var branches = res.Response?.Branches ?? new List<Branch>();
                all.AddRange(branches);
                if (branches.Count < page)
                    break;
                var last = branches[branches.Count - 1];
                request = new ListBranchesRequest { Limit = page, LastUpdatedAt = last.UpdatedAt, LastId = last.Id };
            }
            return all;
        }

        public async Task<List<Tag>> TagsAsync(CancellationToken ct = default)
        {
            var res = await Call<ProxyTagListRequest, ProxyTagListResponse>("ProxyTagList",
                new ProxyTagListRequest { Root = Root, Request = new ListTagsRequest { Limit = 200 } }, ct).ConfigureAwait(false);
            return res.Response?.Tags ?? new List<Tag>();
        }

        public async Task<Tag> FindTagAsync(string name, CancellationToken ct = default)
        {
            const int page = 200;
            const int maxPages = 50;
            var request = new ListTagsRequest { Limit = page };
            for (var i = 0; i < maxPages; i++)
            {
                var res = await Call<ProxyTagListRequest, ProxyTagListResponse>("ProxyTagList",
                    new ProxyTagListRequest { Root = Root, Request = request }, ct).ConfigureAwait(false);
                var tags = res.Response?.Tags ?? new List<Tag>();
                var hit = tags.FirstOrDefault(t => t.Name == name);
                if (hit != null)
                    return hit;
                if (tags.Count < page)
                    return null;
                var last = tags[tags.Count - 1];
                request = new ListTagsRequest { Limit = page, LastCreatedAt = last.CreatedAt, LastId = last.Id };
            }
            return null;
        }

        public async Task<List<CommitLogEntry>> HistoryAsync(string clonePath, int limit = 50, CancellationToken ct = default)
        {
            var request = new CommitLogRequest { Path = Trim(clonePath ?? string.Empty), Limit = limit, Branch = Cache.Branch };
            var head = Cache.Head;
            if (head != null && head.Kind == "tag")
            {
                var tag = await FindTagAsync(head.Name, ct).ConfigureAwait(false);
                if (tag == null)
                    throw new DaemonException(404, "tag " + head.Name + " not found");
                request.StartCommitId = tag.CommitId;
            }
            var res = await Call<ProxyCommitLogRequest, ProxyCommitLogResponse>("ProxyCommitLog",
                new ProxyCommitLogRequest { Root = Root, Request = request }, ct).ConfigureAwait(false);
            return res.Response?.Commits ?? new List<CommitLogEntry>();
        }

        public async Task<ConflictsResponse> ConflictsAsync(CancellationToken ct = default)
        {
            var res = await Call<ConflictsRequest, ConflictsResponse>(
                "Conflicts", new ConflictsRequest { Root = Root }, ct).ConfigureAwait(false);
            res.Files = res.Files ?? new List<ConflictFile>();
            return res;
        }

        public async Task ResolveAsync(string path, string choice, byte[] content = null, CancellationToken ct = default)
        {
            var status = await Call<ResolveConflictRequest, StatusResponse>("ResolveConflict",
                new ResolveConflictRequest { Root = Root, Path = path, Choice = choice, Content = content }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public async Task<byte[]> ReadRevisionAsync(string revision, string path, CancellationToken ct = default)
        {
            if (string.IsNullOrEmpty(revision))
                return null;
            var l = Acquire();
            try
            {
                var req = new ReadBlobRequest { Root = Root, Blob = new BlobRef { Source = "revision", Revision = revision, Path = path } };
                using (var buffer = new MemoryStream())
                {
                    await foreach (var ev in l.Client.StreamAsync<ReadBlobRequest, BlobEvent>("ReadBlob", req, ct).ConfigureAwait(false))
                    {
                        if (ev.Failure != null)
                        {
                            if (ev.Failure.Code == 404)
                                return null;
                            throw new DaemonException(ev.Failure.Code, ev.Failure.Message);
                        }
                        if (ev.Data != null)
                            buffer.Write(ev.Data, 0, ev.Data.Length);
                    }
                    return buffer.ToArray();
                }
            }
            catch (DaemonException e) when (e.Code == 404)
            {
                return null;
            }
            finally
            {
                Release(l);
            }
        }

        public async Task<bool> MergeToolResolveAsync(ConflictsResponse set, string path, string toolPath,
            IMergeProcess process, string workDir, CancellationToken ct = default)
        {
            var baseData = await ReadRevisionAsync(set.Base?.Id, path, ct).ConfigureAwait(false);
            var theirs = await ReadRevisionAsync(set.Theirs?.Id, path, ct).ConfigureAwait(false);
            var ours = await ReadRevisionAsync(set.Ours?.Id, path, ct).ConfigureAwait(false);
            if (theirs == null || ours == null)
                return false;
            var merged = await MergeTool.RunAsync(process, toolPath, workDir, baseData, theirs, ours, ct).ConfigureAwait(false);
            if (merged == null)
                return false;
            await ResolveAsync(path, "content", merged, ct).ConfigureAwait(false);
            return true;
        }

        public Task<OpResult> AbortMergeAsync(Action<OpEvent> onEvent, CancellationToken ct = default)
        {
            return RunOp("Merge", new MergeOpRequest { Root = Root, Abort = true }, onEvent, ct);
        }

        public Task<OpResult> RevertStepAsync(string step, Action<OpEvent> onEvent, CancellationToken ct = default)
        {
            var req = new RevertOpRequest
            {
                Root = Root,
                Abort = step == "abort",
                Continue = step == "continue",
                Skip = step == "skip",
            };
            return RunOp("Revert", req, onEvent, ct);
        }

        public async Task<List<MetaWarning>> MetaWarningsAsync(CancellationToken ct = default)
        {
            var res = await Call<MetaWarningsRequest, MetaWarningsResponse>(
                "MetaWarnings", new MetaWarningsRequest { Root = Root }, ct).ConfigureAwait(false);
            return res.Warnings ?? new List<MetaWarning>();
        }

        public string DesktopLink(string tab)
        {
            var url = Repo?.Url;
            if (string.IsNullOrEmpty(url))
                return null;
            var cut = url.IndexOf("://", StringComparison.Ordinal);
            var rest = cut >= 0 ? url.Substring(cut + 3) : url;
            return "nipa://" + rest.TrimEnd('/') + "/" + tab;
        }

        public List<string> Expand(IEnumerable<string> clonePaths)
        {
            var result = new List<string>();
            var seen = new HashSet<string>();
            foreach (var raw in clonePaths)
            {
                var p = ProjectPaths.Normalize(raw).Trim('/');
                if (p.Length > 0 && seen.Add(p))
                    result.Add(p);
                var other = ProjectPaths.Counterpart(p);
                if (p.Length > 0 && Cache.Has(other) && seen.Add(other))
                    result.Add(other);
            }
            return result;
        }

        async Task<OpResult> RunOp<TReq>(string method, TReq req, Action<OpEvent> onEvent, CancellationToken ct)
        {
            var l = Acquire();
            OpResult result;
            try
            {
                result = await Ops.RunAsync(l.Client, method, req, onEvent, ct).ConfigureAwait(false);
            }
            finally
            {
                Release(l);
            }
            await RefreshAsync(ct).ConfigureAwait(false);
            await TryRefreshLocksAsync(ct).ConfigureAwait(false);
            return result;
        }

        async Task WatchLoopAsync(CancellationToken ct)
        {
            while (!ct.IsCancellationRequested)
            {
                try
                {
                    var l = Acquire();
                    try
                    {
                        var stream = l.Client.StreamAsync<WatchChangesRequest, RepoChange>(
                            "WatchChanges", new WatchChangesRequest { Root = Root }, ct);
                        await foreach (var _ in stream.ConfigureAwait(false))
                            await RefreshAsync(ct).ConfigureAwait(false);
                    }
                    finally
                    {
                        Release(l);
                    }
                }
                catch (OperationCanceledException)
                {
                    return;
                }
                catch (Exception e)
                {
                    WatchFailed?.Invoke(e);
                }
                try
                {
                    await Task.Delay(ReconnectDelay, ct).ConfigureAwait(false);
                    if (reconnect != null)
                        await ReconnectAsync(ct).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    return;
                }
                catch (Exception e)
                {
                    WatchFailed?.Invoke(e);
                }
            }
        }

        async Task ActivityLoopAsync(CancellationToken ct)
        {
            while (!ct.IsCancellationRequested)
            {
                try
                {
                    var l = Acquire();
                    try
                    {
                        var req = new WatchEventsRequest { Root = Root, IntervalSeconds = (int)PollInterval.TotalSeconds };
                        await foreach (var a in l.Client.StreamAsync<WatchEventsRequest, RepoActivity>("WatchEvents", req, ct).ConfigureAwait(false))
                            await OnActivityAsync(a, ct).ConfigureAwait(false);
                    }
                    finally
                    {
                        Release(l);
                    }
                }
                catch (OperationCanceledException)
                {
                    return;
                }
                catch (Exception e)
                {
                    WatchFailed?.Invoke(e);
                }
                try
                {
                    await Task.Delay(ReconnectDelay, ct).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    return;
                }
            }
        }

        async Task OnActivityAsync(RepoActivity a, CancellationToken ct)
        {
            Incoming = a.Incoming;
            ReviewRequests = a.ReviewRequests;
            if (a.Events != null && a.Events.Any(e => e.Kind == "lock_released"))
                await TryRefreshLocksAsync(ct).ConfigureAwait(false);
            Activity?.Invoke(a);
        }

        async Task PollLoopAsync(CancellationToken ct)
        {
            while (!ct.IsCancellationRequested)
            {
                try
                {
                    await Task.Delay(PollInterval, ct).ConfigureAwait(false);
                    await RefreshAsync(ct).ConfigureAwait(false);
                    await TryRefreshLocksAsync(ct).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    return;
                }
                catch (Exception e)
                {
                    WatchFailed?.Invoke(e);
                }
            }
        }

        public void Stop()
        {
            cts.Cancel();
            try
            {
                Task.WaitAll(new[] { watcher, poller, activity }.Where(t => t != null).ToArray(), TimeSpan.FromSeconds(2));
            }
            catch (AggregateException)
            {
            }
        }

        public void Dispose()
        {
            Stop();
            cts.Dispose();
            Lease current;
            lock (gate)
                current = lease;
            Retire(current);
        }

        Task<TRes> Call<TReq, TRes>(string method, TReq request, CancellationToken ct)
        {
            return CallLeased<TReq, TRes>(method, request, ct);
        }

        async Task<TRes> CallLeased<TReq, TRes>(string method, TReq request, CancellationToken ct)
        {
            var l = Acquire();
            try
            {
                return await l.Client.CallAsync<TReq, TRes>(method, request, ct).ConfigureAwait(false);
            }
            finally
            {
                Release(l);
            }
        }

        Lease Acquire()
        {
            lock (gate)
            {
                lease.Users++;
                return lease;
            }
        }

        void Release(Lease l)
        {
            bool dispose;
            lock (gate)
            {
                l.Users--;
                dispose = l.Retired && l.Users == 0;
            }
            if (dispose)
                l.Client.Dispose();
        }

        void Retire(Lease l)
        {
            bool dispose;
            lock (gate)
            {
                l.Retired = true;
                dispose = l.Users == 0;
            }
            if (dispose)
                l.Client.Dispose();
        }

        sealed class Lease
        {
            public DaemonClient Client;
            public int Users;
            public bool Retired;
        }
    }
}
