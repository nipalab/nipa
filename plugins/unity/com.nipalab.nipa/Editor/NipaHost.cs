using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
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
        public static float BusyProgress { get; private set; } = -1f;

        public static event Action Changed;

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
            Changed?.Invoke();
            Task.Run(async () =>
            {
                NipaSession session = null;
                try
                {
                    var conn = await Connector.ConnectAsync(
                        new ConnectorOptions { Binary = binary }, token).ConfigureAwait(false);
                    var options = new ConnectorOptions { Binary = binary };
                    session = new NipaSession(conn.Client, paths, async c =>
                        (await Connector.ConnectAsync(options, c).ConfigureAwait(false)).Client);
                    session.Cache.Changed += _ => MainThread.Post(OnCacheChanged);
                    session.WatchFailed += e => MainThread.Post(() => { Error = e.Message; Changed?.Invoke(); });
                    await session.StartAsync(token).ConfigureAwait(false);
                    MainThread.Post(() =>
                    {
                        if (gen != generation)
                        {
                            session.Dispose();
                            return;
                        }
                        Session = session;
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

        public static void Run(string title, Func<NipaSession, Action<OpEvent>, Task> op, bool refreshAssets = false)
        {
            var session = Session;
            if (session == null || Busy != null)
                return;
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
                    error = e.Message;
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
                });
            });
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
            };
        }
    }
}
