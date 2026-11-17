using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Session;
using UnityEditor;
using UnityEditor.VersionControl;

namespace Nipa.Editor
{
    [InitializeOnLoad]
    internal static class NipaHost
    {
        static CancellationTokenSource cts;
        static int generation;
        static bool repaintQueued;

        public static NipaSession Session { get; private set; }
        public static bool Connecting { get; private set; }
        public static string Error { get; private set; }
        public static string Busy { get; private set; }
        public static string DaemonVersion { get; private set; }
        public static float BusyProgress { get; private set; } = -1f;

        public static event Action Changed;

        public static List<string> Notices { get; } = new List<string>();

        const string WatchesKey = "Nipa.LockWatches";
        static LockWatches watches;

        public static LockWatches Watches
        {
            get
            {
                if (watches != null)
                    return watches;
                watches = LockWatches.Parse(EditorUserSettings.GetConfigValue(WatchesKey));
                watches.Changed += () =>
                {
                    EditorUserSettings.SetConfigValue(WatchesKey, watches.Serialize());
                    Changed?.Invoke();
                };
                return watches;
            }
        }

        public static bool SaveOpenScenes()
        {
            if (UnityEngine.Application.isBatchMode)
                return true;
            return UnityEditor.SceneManagement.EditorSceneManager.SaveCurrentModifiedScenesIfUserWantsTo();
        }

        public static void Sync(string title, string operation, string branch, string tag,
            Func<NipaSession, Action<OpEvent>, Task> op, Action onSuccess = null)
        {
            var session = Session;
            if (session == null || Busy != null || !Allowed(title, true) || !SaveOpenScenes())
                return;
            Busy = "Checking local changes";
            BusyProgress = -1f;
            Changed?.Invoke();
            Task.Run(async () =>
            {
                List<string> overwritten = null;
                try
                {
                    overwritten = await session.SyncPlanAsync(branch, tag).ConfigureAwait(false);
                }
                catch (Exception e)
                {
                    UnityEngine.Debug.LogWarning("[Nipa] Could not check local changes before " + operation + ": " + e.Message);
                }
                MainThread.Post(() =>
                {
                    Busy = null;
                    Changed?.Invoke();
                    if (overwritten == null || overwritten.Count == 0)
                    {
                        Run(title, op, refreshAssets: true, onSuccess: onSuccess);
                        return;
                    }
                    var list = string.Join("\n", overwritten.Take(10).Select(p => "• " + p)) + (overwritten.Count > 10 ? "\n…" : string.Empty);
                    var choice = EditorUtility.DisplayDialogComplex("Nipa: " + title,
                        title + " would replace your local changes to " + overwritten.Count + " file(s):\n" + list
                        + "\n\nShelve them first? Apply the shelf afterwards from the Shelves tab.",
                        "Shelve and continue", "Cancel", "Overwrite them");
                    if (choice == 1)
                        return;
                    if (choice == 2)
                    {
                        Run(title, op, refreshAssets: true, onSuccess: onSuccess);
                        return;
                    }
                    var name = OpSummary.ShelfName(operation, DateTime.Now);
                    Run(title, async (s, on) =>
                    {
                        await s.ShelveAsync(name, overwritten, true, on).ConfigureAwait(false);
                        await op(s, on).ConfigureAwait(false);
                    }, refreshAssets: true, onSuccess: onSuccess);
                });
            });
        }

        public static void NotifyChanged() => Changed?.Invoke();

        public static MaintenanceNotice Maintenance => Session?.MaintenanceNotice(DateTimeOffset.UtcNow);

        public static bool Allowed(string title, bool sync)
        {
            var notice = Maintenance;
            if (notice == null || !(sync ? notice.BlocksSync : notice.BlocksWrites))
                return true;
            NipaLocks.Tell("Nipa: " + title, notice.Text);
            return false;
        }

        public static void DismissNotices()
        {
            Notices.Clear();
            Changed?.Invoke();
        }

        public static void ApplySettings()
        {
            Session?.ApplySettings(NipaSettingsStore.LoadProject());
            OnCacheChanged();
        }

        public static bool IsManaged(string unityPath)
        {
            return !string.IsNullOrEmpty(unityPath)
                && (unityPath == "Assets" || unityPath.StartsWith("Assets/", StringComparison.Ordinal)
                    || unityPath.StartsWith("Packages/", StringComparison.Ordinal));
        }

        static NipaHost()
        {
            AssemblyReloadEvents.beforeAssemblyReload += Deactivate;
            EditorApplication.delayCall += () =>
            {
                if (VersionControlManager.activeVersionControlObject is NipaVersionControl)
                    Activate();
            };
        }

        public static void Activate()
        {
            if (Session != null || Connecting)
                return;
            var paths = ProjectPaths.Discover(NipaSettingsStore.ProjectDir);
            if (paths == null)
            {
                Error = "This Unity project is not inside a Nipa clone (.nipa/config not found).";
                Changed?.Invoke();
                return;
            }
            Connecting = true;
            Error = null;
            var gen = ++generation;
            cts = new CancellationTokenSource();
            var token = cts.Token;
            var binary = NipaSettingsStore.BinaryPath;
            var poll = TimeSpan.FromSeconds(NipaSettingsStore.LoadProject().PollIntervalSeconds);
            Changed?.Invoke();
            Task.Run(async () =>
            {
                NipaSession session = null;
                try
                {
                    var conn = await Connector.ConnectAsync(
                        new ConnectorOptions { Binary = binary }, token).ConfigureAwait(false);
                    var version = conn.Version;
                    var options = new ConnectorOptions { Binary = binary };
                    session = new NipaSession(conn.Client, paths, async c =>
                        (await Connector.ConnectAsync(options, c).ConfigureAwait(false)).Client)
                    {
                        PollInterval = poll,
                    };
                    session.ApplySettings(NipaSettingsStore.LoadProject());
                    session.Cache.Changed += _ => MainThread.Post(OnCacheChanged);
                    session.LocksChanged += () => MainThread.Post(OnCacheChanged);
                    session.Activity += a => MainThread.Post(() => OnActivity(a));
                    session.Healthy += () => MainThread.Post(ClearError);
                    session.WatchFailed += e => MainThread.Post(() => { Error = e.Message; Changed?.Invoke(); });
                    await session.StartAsync(token).ConfigureAwait(false);
                    MainThread.Post(() =>
                    {
                        if (gen != generation)
                        {
                            Task.Run(session.Dispose);
                            return;
                        }
                        Session = session;
                        DaemonVersion = version;
                        Connecting = false;
                        OnCacheChanged();
                    });
                }
                catch (OperationCanceledException)
                {
                    session?.Dispose();
                }
                catch (Exception e)
                {
                    session?.Dispose();
                    MainThread.Post(() =>
                    {
                        if (gen != generation)
                            return;
                        Connecting = false;
                        Error = e.Message;
                        Changed?.Invoke();
                    });
                }
            });
        }

        public static void Deactivate()
        {
            generation++;
            cts?.Cancel();
            var session = Session;
            Session = null;
            Connecting = false;
            if (session != null)
                Task.Run(session.Dispose);
            Changed?.Invoke();
        }

        public static void Reconnect()
        {
            Deactivate();
            Activate();
        }

        public static void Refresh()
        {
            var session = Session;
            if (session == null)
                return;
            Task.Run(async () =>
            {
                try
                {
                    await session.RefreshAsync().ConfigureAwait(false);
                    await session.RefreshLocksAsync().ConfigureAwait(false);
                }
                catch (Exception e)
                {
                    MainThread.Post(() => { Error = e.Message; Changed?.Invoke(); });
                }
            });
        }

        public static List<string> SelectedClonePaths()
        {
            var session = Session;
            if (session == null)
                return new List<string>();
            return Selection.assetGUIDs
                .Select(AssetDatabase.GUIDToAssetPath)
                .Where(p => !string.IsNullOrEmpty(p))
                .Select(session.Paths.ToClonePath)
                .ToList();
        }

        public static void Run(string title, Func<NipaSession, Action<OpEvent>, Task> op, bool refreshAssets = false, Action onSuccess = null)
        {
            var session = Session;
            if (session == null)
                return;
            if (Busy != null)
            {
                NipaLocks.Tell("Nipa: " + title, Busy + " is still running. Try again when it has finished.");
                return;
            }
            Busy = title;
            BusyProgress = -1f;
            Changed?.Invoke();
            Task.Run(async () =>
            {
                string error = null;
                try
                {
                    await op(session, ev => MainThread.Post(() => OnOpEvent(ev))).ConfigureAwait(false);
                }
                catch (Exception e)
                {
                    error = MaintenanceNotes.ErrorText(e);
                }
                MainThread.Post(() =>
                {
                    Busy = null;
                    BusyProgress = -1f;
                    if (error != null)
                        EditorUtility.DisplayDialog("Nipa: " + title, error, "OK");
                    else if (refreshAssets)
                        AssetDatabase.Refresh();
                    OnCacheChanged();
                    if (error == null)
                        onSuccess?.Invoke();
                });
            });
        }

        static void OnActivity(RepoActivity a)
        {
            if (a.Events != null)
            {
                foreach (var e in a.Events)
                {
                    var text = OpSummary.Describe(e);
                    if (text == null)
                        continue;
                    Notices.Add(text);
                    UnityEngine.Debug.Log("[Nipa] " + text);
                    if (e.Kind == "lock_requested")
                        PromptRelease(e);
                    else if (e.Kind == "lock_released")
                        PromptWatched(e);
                }
                while (Notices.Count > 20)
                    Notices.RemoveAt(0);
            }
            OnCacheChanged();
        }

        static void PromptRelease(TeamEvent e)
        {
            if (string.IsNullOrEmpty(e.Path) || UnityEngine.Application.isBatchMode)
                return;
            var path = e.Path;
            var note = string.IsNullOrWhiteSpace(e.Message) ? string.Empty : "\n\n\"" + e.Message + "\"";
            EditorApplication.delayCall += () =>
            {
                if (EditorUtility.DisplayDialog("Nipa: release requested",
                    (string.IsNullOrEmpty(e.Actor) ? "A teammate" : e.Actor) + " asks you to release your lock on " + path + "." + note,
                    "Release lock", "Keep it"))
                    NipaLocks.Unlock(new List<string> { path });
            };
        }

        static void PromptWatched(TeamEvent e)
        {
            if (string.IsNullOrEmpty(e.Path) || UnityEngine.Application.isBatchMode)
                return;
            var hits = Watches.TakeHits(e.Path);
            if (hits.Count == 0)
                return;
            EditorApplication.delayCall += () =>
            {
                var list = string.Join("\n", hits.Select(p => "• " + p));
                if (EditorUtility.DisplayDialog("Nipa: lock released", "Free to lock now:\n" + list, "Lock now", "Later"))
                    NipaLocks.Lock(hits);
            };
        }

        static void ClearError()
        {
            if (Error == null)
                return;
            Error = null;
            Changed?.Invoke();
        }

        static void OnOpEvent(OpEvent ev)
        {
            if (ev.Progress != null && ev.Progress.BytesTotal > 0)
                BusyProgress = (float)ev.Progress.BytesDone / ev.Progress.BytesTotal;
            else if (ev.Progress != null && ev.Progress.ObjectsTotal > 0)
                BusyProgress = (float)ev.Progress.ObjectsDone / ev.Progress.ObjectsTotal;
            Changed?.Invoke();
        }

        static void OnCacheChanged()
        {
            if (repaintQueued)
                return;
            repaintQueued = true;
            EditorApplication.delayCall += () =>
            {
                repaintQueued = false;
                Changed?.Invoke();
                EditorApplication.RepaintProjectWindow();
                EditorApplication.RepaintHierarchyWindow();
                UnityEditorInternal.InternalEditorUtility.RepaintAllViews();
            };
        }
    }
}
