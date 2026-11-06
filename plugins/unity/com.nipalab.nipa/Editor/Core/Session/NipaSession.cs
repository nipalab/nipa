using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Status;

namespace Nipa.Editor.Core.Session
{
    public sealed class NipaSession : IDisposable
    {
        readonly Func<CancellationToken, Task<DaemonClient>> reconnect;
        DaemonClient client;
        readonly CancellationTokenSource cts = new CancellationTokenSource();
        Task watcher;

        public ProjectPaths Paths { get; }
        public StatusCache Cache { get; } = new StatusCache();
        public string Root => Paths.CloneRoot;
        public RepoInfo Repo { get; private set; }
        public TimeSpan ReconnectDelay { get; set; } = TimeSpan.FromSeconds(2);

        public event Action<Exception> WatchFailed;

        public NipaSession(DaemonClient client, ProjectPaths paths,
            Func<CancellationToken, Task<DaemonClient>> reconnect = null)
        {
            this.client = client;
            this.reconnect = reconnect;
            Paths = paths;
        }

        public async Task StartAsync(CancellationToken ct = default)
        {
            await WatchRepoAsync(ct).ConfigureAwait(false);
            watcher = Task.Run(() => WatchLoopAsync(cts.Token));
        }

        async Task WatchRepoAsync(CancellationToken ct)
        {
            var watched = await client.CallAsync<WatchRepoRequest, WatchRepoResponse>(
                "WatchRepo", new WatchRepoRequest { Root = Root }, ct).ConfigureAwait(false);
            Repo = watched.Repo;
            await RefreshAsync(ct).ConfigureAwait(false);
        }

        async Task ReconnectAsync(CancellationToken ct)
        {
            var fresh = await reconnect(ct).ConfigureAwait(false);
            var old = Interlocked.Exchange(ref client, fresh);
            old.Dispose();
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
            var status = await client.CallAsync<StatusRequest, StatusResponse>(
                "Status", new StatusRequest { Root = Root }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public async Task StageAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var status = await client.CallAsync<StageRequest, StatusResponse>(
                "Stage", new StageRequest { Root = Root, Add = Expand(clonePaths) }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public async Task UnstageAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var status = await client.CallAsync<StageRequest, StatusResponse>(
                "Stage", new StageRequest { Root = Root, Unstage = Expand(clonePaths) }, ct).ConfigureAwait(false);
            Cache.Apply(status);
        }

        public async Task RestoreAsync(IEnumerable<string> clonePaths, CancellationToken ct = default)
        {
            var status = await client.CallAsync<RestoreRequest, StatusResponse>(
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

        public async Task<List<MetaWarning>> MetaWarningsAsync(CancellationToken ct = default)
        {
            var res = await client.CallAsync<MetaWarningsRequest, MetaWarningsResponse>(
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
            var result = await Ops.RunAsync(client, method, req, onEvent, ct).ConfigureAwait(false);
            await RefreshAsync(ct).ConfigureAwait(false);
            return result;
        }

        async Task WatchLoopAsync(CancellationToken ct)
        {
            while (!ct.IsCancellationRequested)
            {
                try
                {
                    var stream = client.StreamAsync<WatchChangesRequest, RepoChange>(
                        "WatchChanges", new WatchChangesRequest { Root = Root }, ct);
                    await foreach (var _ in stream.ConfigureAwait(false))
                        await RefreshAsync(ct).ConfigureAwait(false);
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

        public void Stop()
        {
            cts.Cancel();
            try
            {
                watcher?.Wait(TimeSpan.FromSeconds(2));
            }
            catch (AggregateException)
            {
            }
        }

        public void Dispose()
        {
            Stop();
            cts.Dispose();
            client.Dispose();
        }
    }
}
