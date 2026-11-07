using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Status;
using UnityEditor;
using UnityEngine;

namespace Nipa.Editor
{
    public sealed class NipaWindow : EditorWindow
    {
        readonly HashSet<string> selectedChanges = new HashSet<string>();
        readonly HashSet<string> selectedStaged = new HashSet<string>();
        List<MetaWarning> warnings = new List<MetaWarning>();
        bool warningsLoading;
        object warningsSession;
        int warningsVersion = -1;
        object rowsSession;
        int rowsVersion = -1;
        List<Row> changeRows = new List<Row>();
        List<Row> stagedRows = new List<Row>();
        int conflictCount;
        bool blocked;
        string message = string.Empty;
        Vector2 scroll;
        int tab;

        public static void Open()
        {
            var w = GetWindow<NipaWindow>("Nipa");
            w.minSize = new Vector2(320, 360);
            w.Show();
        }

        const int MaxRows = 500;

        void OnEnable()
        {
            NipaHost.Changed += OnHostChanged;
        }

        void OnDisable()
        {
            NipaHost.Changed -= OnHostChanged;
        }

        void OnHostChanged()
        {
            Repaint();
        }

        void OnFocus()
        {
            NipaHost.Refresh();
        }

        void OnGUI()
        {
            DrawHeader();
            var session = NipaHost.Session;
            if (session == null)
                return;
            RebuildRows(session);
            LoadWarnings(session);
            DrawBanners();

            tab = GUILayout.Toolbar(tab, new[] { "Changes", "Locks (" + session.Locks.Mine.Count() + ")" });
            scroll = EditorGUILayout.BeginScrollView(scroll);
            if (tab == 0)
            {
                DrawList("Changes", changeRows, selectedChanges, "Stage", "Stage all", paths =>
                    NipaHost.Run("Stage", (s, _) => s.StageAsync(paths)));
                DrawList("Staged", stagedRows, selectedStaged, "Unstage", "Unstage all", paths =>
                    NipaHost.Run("Unstage", (s, _) => s.UnstageAsync(paths)));
            }
            else
            {
                DrawLocks(session);
            }
            EditorGUILayout.EndScrollView();

            if (tab == 0)
                DrawFooter(stagedRows.Count);
        }

        void RebuildRows(Core.Session.NipaSession session)
        {
            if (ReferenceEquals(rowsSession, session) && rowsVersion == session.Cache.Version)
                return;
            rowsSession = session;
            rowsVersion = session.Cache.Version;
            var snapshot = session.Cache.Snapshot();
            stagedRows = Rows(snapshot, s => s.HasFlag(FileState.Staged) || s.HasFlag(FileState.Added));
            changeRows = Rows(snapshot, s => !s.HasFlag(FileState.Staged) && !s.HasFlag(FileState.Added)
                && (s & (FileState.Modified | FileState.Deleted | FileState.Missing | FileState.Untracked)) != 0);
            conflictCount = snapshot.Count(kv => kv.Value.HasFlag(FileState.Conflict) && !kv.Value.HasFlag(FileState.Resolved));
            blocked = session.Cache.Head != null || conflictCount > 0;
        }

        void DrawHeader()
        {
            var session = NipaHost.Session;
            using (new EditorGUILayout.HorizontalScope(EditorStyles.toolbar))
            {
                if (session != null)
                    GUILayout.Label(session.Cache.Head != null
                        ? "tag " + session.Cache.Head.Name
                        : session.Cache.Branch ?? "?", EditorStyles.boldLabel);
                else
                    GUILayout.Label(NipaHost.Connecting ? "Connecting..." : "Disconnected");
                GUILayout.FlexibleSpace();
                if (GUILayout.Button("Refresh", EditorStyles.toolbarButton))
                    NipaHost.Refresh();
                using (new EditorGUI.DisabledScope(session == null || NipaHost.Busy != null))
                {
                    if (GUILayout.Button("Update", EditorStyles.toolbarButton))
                        NipaHost.Run("Update", (s, on) => s.UpdateAsync(on), refreshAssets: true);
                }
                using (new EditorGUI.DisabledScope(session?.DesktopLink("changes") == null))
                {
                    if (GUILayout.Button("Open in Nipa Desktop", EditorStyles.toolbarButton))
                        Application.OpenURL(session.DesktopLink("changes"));
                }
            }

            if (session == null)
            {
                if (NipaHost.Error != null)
                    EditorGUILayout.HelpBox(NipaHost.Error, MessageType.Warning);
                else if (!NipaHost.Connecting)
                    EditorGUILayout.HelpBox("Set Project Settings > Editor > Version Control > Mode to Nipa.", MessageType.Info);
                if (GUILayout.Button("Connect"))
                    NipaHost.Reconnect();
            }
        }

        void DrawBanners()
        {
            var cache = NipaHost.Session.Cache;
            if (cache.Head != null)
                EditorGUILayout.HelpBox("HEAD is detached at " + cache.Head.Kind + " \"" + cache.Head.Name + "\". Switch to a branch to push.", MessageType.Warning);
            if (!string.IsNullOrEmpty(cache.Pending))
                EditorGUILayout.HelpBox("A " + cache.Pending + " is waiting. Finish it in Nipa Desktop.", MessageType.Warning);
            if (conflictCount > 0)
                EditorGUILayout.HelpBox(conflictCount + " file(s) in conflict. Resolve them in Nipa Desktop.", MessageType.Error);
            if (NipaHost.Error != null)
                EditorGUILayout.HelpBox(NipaHost.Error, MessageType.Warning);
            DrawActivity();
            if (warnings.Count > 0)
            {
                var text = string.Join("\n", warnings.Take(6).Select(w => "• " + w.Message));
                if (warnings.Count > 6)
                    text += "\n… and " + (warnings.Count - 6) + " more";
                EditorGUILayout.HelpBox(".meta warnings:\n" + text, MessageType.Warning);
            }
        }

        void DrawActivity()
        {
            var session = NipaHost.Session;
            if (session.Incoming > 0)
            {
                EditorGUILayout.HelpBox("Out of date: " + session.Incoming + " new commit(s) on " + session.Cache.Branch + ".", MessageType.Info);
                using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
                {
                    if (GUILayout.Button("Update"))
                        NipaHost.Run("Update", (s, on) => s.UpdateAsync(on), refreshAssets: true);
                }
            }
            if (session.ReviewRequests > 0)
            {
                EditorGUILayout.HelpBox(session.ReviewRequests + " merge request(s) waiting for your review.", MessageType.Info);
                if (session.DesktopLink("merge-requests") != null && GUILayout.Button("Open in Nipa Desktop"))
                    Application.OpenURL(session.DesktopLink("merge-requests"));
            }
            if (NipaHost.Notices.Count == 0)
                return;
            EditorGUILayout.HelpBox(string.Join("\n", NipaHost.Notices.Skip(NipaHost.Notices.Count - 5)), MessageType.None);
            if (GUILayout.Button("Dismiss"))
                NipaHost.DismissNotices();
        }

        void DrawLocks(Core.Session.NipaSession session)
        {
            var policy = session.Locks;
            var locks = policy.Locks.Where(policy.InScope).OrderBy(l => l.Path, System.StringComparer.Ordinal).ToList();
            EditorGUILayout.LabelField("Locks on " + (policy.DefaultBranch ? "the default branch" : policy.Branch) + " (" + locks.Count + ")", EditorStyles.boldLabel);
            foreach (var l in locks)
            {
                using (new EditorGUILayout.HorizontalScope())
                {
                    GUILayout.Label(new GUIContent(l.Path, l.Path), GUILayout.MinWidth(80));
                    GUILayout.FlexibleSpace();
                    GUILayout.Label(policy.IsMine(l) ? "you" : l.HeldByName, EditorStyles.miniLabel, GUILayout.Width(80));
                    using (new EditorGUI.DisabledScope(!policy.CanRelease(l) || NipaHost.Busy != null))
                    {
                        if (GUILayout.Button("Release", GUILayout.Width(64)))
                            NipaHost.Run("Release", (s, _) => s.ReleaseAsync(l));
                    }
                }
            }
            if (locks.Count == 0)
                EditorGUILayout.HelpBox("No locks.", MessageType.None);
        }

        void PushWithLockCheck(string msg)
        {
            NipaHost.Run("Push", async (s, on) =>
            {
                var check = await s.CheckPushAsync();
                if (check.Theirs.Count > 0)
                {
                    var lines = check.Theirs.Select(f => "• " + f.Path + " — locked by " + (string.IsNullOrEmpty(f.HeldByName) ? f.HeldBy : f.HeldByName));
                    throw new System.InvalidOperationException("Locked by someone else:\n" + string.Join("\n", lines));
                }
                if (check.Needed.Count > 0)
                {
                    MainThread.Post(() => PromptLock(check.Needed, msg));
                    return;
                }
                await s.PushAsync(msg, on);
            });
        }

        void PromptLock(List<string> needed, string msg)
        {
            var list = string.Join("\n", needed.Take(10).Select(p => "• " + p)) + (needed.Count > 10 ? "\n…" : string.Empty);
            if (!EditorUtility.DisplayDialog("Nipa: lock required",
                "These changed binary files need a lock before they can be pushed:\n" + list, "Lock " + needed.Count + " files", "Cancel"))
            {
                message = msg;
                return;
            }
            NipaLocks.Lock(needed, () => PushWithLockCheck(msg));
        }

        void DrawList(string title, List<Row> rows, HashSet<string> selected, string actionLabel, string allLabel,
            System.Action<List<string>> run)
        {
            var visible = new HashSet<string>(rows.Take(MaxRows).Select(r => r.Path));
            selected.RemoveWhere(p => !visible.Contains(p));
            EditorGUILayout.LabelField(title + " (" + rows.Count + ")", EditorStyles.boldLabel);
            foreach (var row in rows.Take(MaxRows))
            {
                using (new EditorGUILayout.HorizontalScope())
                {
                    var on = EditorGUILayout.ToggleLeft(row.Label, selected.Contains(row.Path));
                    if (on)
                        selected.Add(row.Path);
                    else
                        selected.Remove(row.Path);
                    GUILayout.Label(row.Tag, EditorStyles.miniLabel, GUILayout.Width(64));
                }
            }
            if (rows.Count == 0)
                return;
            if (rows.Count > MaxRows)
                EditorGUILayout.HelpBox("Showing the first " + MaxRows + " of " + rows.Count + ". \"" + allLabel + "\" applies to all of them.", MessageType.None);
            using (new EditorGUILayout.HorizontalScope())
            using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
            {
                using (new EditorGUI.DisabledScope(selected.Count == 0))
                {
                    if (GUILayout.Button(actionLabel))
                        run(selected.ToList());
                    if (title == "Changes" && GUILayout.Button("Restore"))
                        RestoreSelected(rows, selected);
                }
                if (GUILayout.Button(allLabel))
                    run(rows.Select(r => r.Path).ToList());
            }
        }

        void RestoreSelected(List<Row> rows, HashSet<string> selected)
        {
            var paths = rows.Where(r => selected.Contains(r.Path) && !r.State.HasFlag(FileState.Untracked))
                .Select(r => r.Path).ToList();
            if (paths.Count == 0)
            {
                EditorUtility.DisplayDialog("Nipa: Restore", "Untracked files cannot be restored.", "OK");
                return;
            }
            if (!EditorUtility.DisplayDialog("Nipa: Restore", "Discard local changes to " + paths.Count + " file(s)?", "Restore", "Cancel"))
                return;
            NipaHost.Run("Restore", async (s, _) => { await s.RestoreAsync(paths); }, refreshAssets: true);
        }

        void DrawFooter(int stagedCount)
        {
            if (NipaHost.Busy != null)
            {
                var rect = EditorGUILayout.GetControlRect();
                EditorGUI.ProgressBar(rect, Mathf.Max(NipaHost.BusyProgress, 0f), NipaHost.Busy + "...");
                return;
            }
            EditorGUILayout.LabelField("Message");
            message = EditorGUILayout.TextArea(message, GUILayout.MinHeight(48));
            using (new EditorGUI.DisabledScope(stagedCount == 0 || string.IsNullOrWhiteSpace(message) || blocked))
            {
                if (GUILayout.Button("Push"))
                {
                    var msg = message;
                    message = string.Empty;
                    PushWithLockCheck(msg);
                }
            }
        }

        void LoadWarnings(Core.Session.NipaSession session)
        {
            if (warningsLoading)
                return;
            var version = session.Cache.Version;
            if (ReferenceEquals(warningsSession, session) && warningsVersion == version)
                return;
            warningsSession = session;
            warningsVersion = version;
            warningsLoading = true;
            Task.Run(async () =>
            {
                List<MetaWarning> result = null;
                try
                {
                    result = await session.MetaWarningsAsync().ConfigureAwait(false);
                }
                catch (System.Exception)
                {
                }
                MainThread.Post(() =>
                {
                    warningsLoading = false;
                    if (result != null)
                        warnings = result;
                    Repaint();
                });
            });
        }

        static List<Row> Rows(IReadOnlyDictionary<string, FileState> snapshot, System.Func<FileState, bool> pick)
        {
            var rows = new List<Row>();
            foreach (var kv in snapshot.OrderBy(kv => kv.Key, System.StringComparer.Ordinal))
            {
                if (!pick(kv.Value))
                    continue;
                if (ProjectPaths.IsMeta(kv.Key) && snapshot.TryGetValue(ProjectPaths.AssetOf(kv.Key), out var owner) && pick(owner))
                    continue;
                var hasMeta = snapshot.TryGetValue(ProjectPaths.MetaOf(kv.Key), out var meta) && !ProjectPaths.IsMeta(kv.Key) && pick(meta);
                rows.Add(new Row
                {
                    Path = kv.Key,
                    State = kv.Value,
                    Label = kv.Key + (hasMeta ? "  (+meta)" : string.Empty),
                    Tag = Describe(kv.Value),
                });
            }
            return rows;
        }

        static string Describe(FileState s)
        {
            if (s.HasFlag(FileState.Conflict))
                return "conflict";
            if (s.HasFlag(FileState.Added))
                return "added";
            if (s.HasFlag(FileState.Deleted))
                return "deleted";
            if (s.HasFlag(FileState.Missing))
                return "missing";
            if (s.HasFlag(FileState.Untracked))
                return "new";
            return s.HasFlag(FileState.Staged) ? "staged" : "modified";
        }

        sealed class Row
        {
            public string Path;
            public string Label;
            public string Tag;
            public FileState State;
        }
    }
}
