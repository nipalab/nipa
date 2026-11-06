using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
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
        bool warningsStale = true;
        string message = string.Empty;
        Vector2 scroll;

        public static void Open()
        {
            var w = GetWindow<NipaWindow>("Nipa");
            w.minSize = new Vector2(320, 360);
            w.Show();
        }

        void OnEnable()
        {
            NipaHost.Changed += OnHostChanged;
            warningsStale = true;
        }

        void OnDisable()
        {
            NipaHost.Changed -= OnHostChanged;
        }

        void OnHostChanged()
        {
            warningsStale = true;
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
            LoadWarnings();
            DrawBanners();
            var snapshot = session.Cache.Snapshot();
            var staged = Rows(snapshot, s => s.HasFlag(FileState.Staged) || s.HasFlag(FileState.Added));
            var changes = Rows(snapshot, s => !s.HasFlag(FileState.Staged) && !s.HasFlag(FileState.Added)
                && (s & (FileState.Modified | FileState.Deleted | FileState.Missing | FileState.Untracked)) != 0);

            scroll = EditorGUILayout.BeginScrollView(scroll);
            DrawList("Changes", changes, selectedChanges, "Stage", "Stage all", paths =>
                NipaHost.Run("Stage", (s, _) => s.StageAsync(paths)));
            DrawList("Staged", staged, selectedStaged, "Unstage", "Unstage all", paths =>
                NipaHost.Run("Unstage", (s, _) => s.UnstageAsync(paths)));
            EditorGUILayout.EndScrollView();

            DrawFooter(staged.Count, snapshot);
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
            var conflicts = cache.Snapshot().Count(kv => kv.Value.HasFlag(FileState.Conflict) && !kv.Value.HasFlag(FileState.Resolved));
            if (conflicts > 0)
                EditorGUILayout.HelpBox(conflicts + " file(s) in conflict. Resolve them in Nipa Desktop.", MessageType.Error);
            if (NipaHost.Error != null)
                EditorGUILayout.HelpBox(NipaHost.Error, MessageType.Warning);
            if (warnings.Count > 0)
            {
                var text = string.Join("\n", warnings.Take(6).Select(w => "• " + w.Message));
                if (warnings.Count > 6)
                    text += "\n… and " + (warnings.Count - 6) + " more";
                EditorGUILayout.HelpBox(".meta warnings:\n" + text, MessageType.Warning);
            }
        }

        void DrawList(string title, List<Row> rows, HashSet<string> selected, string actionLabel, string allLabel,
            System.Action<List<string>> run)
        {
            var visible = new HashSet<string>(rows.Select(r => r.Path));
            selected.RemoveWhere(p => !visible.Contains(p));
            EditorGUILayout.LabelField(title + " (" + rows.Count + ")", EditorStyles.boldLabel);
            foreach (var row in rows)
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

        void DrawFooter(int stagedCount, IReadOnlyDictionary<string, FileState> snapshot)
        {
            if (NipaHost.Busy != null)
            {
                var rect = EditorGUILayout.GetControlRect();
                EditorGUI.ProgressBar(rect, Mathf.Max(NipaHost.BusyProgress, 0f), NipaHost.Busy + "...");
                return;
            }
            EditorGUILayout.LabelField("Message");
            message = EditorGUILayout.TextArea(message, GUILayout.MinHeight(48));
            var blocked = NipaHost.Session.Cache.Head != null || snapshot.Values.Any(s => s.HasFlag(FileState.Conflict) && !s.HasFlag(FileState.Resolved));
            using (new EditorGUI.DisabledScope(stagedCount == 0 || string.IsNullOrWhiteSpace(message) || blocked))
            {
                if (GUILayout.Button("Push"))
                {
                    var msg = message;
                    message = string.Empty;
                    NipaHost.Run("Push", (s, on) => s.PushAsync(msg, on));
                }
            }
        }

        void LoadWarnings()
        {
            if (!warningsStale || warningsLoading)
                return;
            warningsStale = false;
            warningsLoading = true;
            var session = NipaHost.Session;
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
