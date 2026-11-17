using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Merge;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Session;
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
        int setupCount;
        ConflictsResponse conflictSet;
        bool conflictsLoading;
        object conflictsSession;
        int conflictsVersion = -1;
        List<CommitLogEntry> history = new List<CommitLogEntry>();
        string historyPath;
        string historyKey;
        bool historyLoading;
        string historyError;
        string historyNext;
        bool historyMoreLoading;
        ShelveListResponse shelves;
        bool shelvesLoading;
        object shelvesSession;
        int shelvesVersion = -1;
        string shelvesError;

        public const int ChangesTab = 0;
        public const int LocksTab = 1;
        public const int ConflictsTab = 2;
        public const int HistoryTab = 3;
        public const int ShelvesTab = 4;
        public const int SetupTab = 5;

        public static void Open()
        {
            var w = GetWindow<NipaWindow>("Nipa");
            w.minSize = new Vector2(320, 360);
            w.Show();
        }

        public static void OpenTab(int index)
        {
            var w = GetWindow<NipaWindow>("Nipa");
            w.tab = index;
            w.Show();
            w.Repaint();
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

        void OnSelectionChange()
        {
            Repaint();
        }

        void OnFocus()
        {
            NipaHost.Refresh();
            NipaBranches.Load(true);
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
            if (Event.current.type == EventType.Layout)
                setupCount = NipaSetup.Check(session).Count;

            tab = GUILayout.Toolbar(tab, new[]
            {
                "Changes",
                "Locks (" + session.Locks.Mine.Count() + ")",
                "Conflicts" + (conflictCount > 0 ? " (" + conflictCount + ")" : string.Empty),
                "History",
                "Shelves" + (shelves != null && shelves.Shelves.Count > 0 ? " (" + shelves.Shelves.Count + ")" : string.Empty),
                "Setup" + (setupCount > 0 ? " (" + setupCount + ")" : string.Empty),
            });
            if (tab == ConflictsTab)
                LoadConflicts(session);
            if (tab == ShelvesTab)
                LoadShelves(session);
            scroll = EditorGUILayout.BeginScrollView(scroll);
            if (tab == ChangesTab)
            {
                DrawList("Changes", changeRows, selectedChanges, "Stage", "Stage all", paths =>
                    NipaHost.Run("Stage", (s, _) => s.StageAsync(paths)));
                DrawList("Staged", stagedRows, selectedStaged, "Unstage", "Unstage all", paths =>
                    NipaHost.Run("Unstage", (s, _) => s.UnstageAsync(paths)));
            }
            else if (tab == LocksTab)
            {
                DrawLocks(session);
            }
            else if (tab == ConflictsTab)
            {
                DrawConflicts(session);
            }
            else if (tab == HistoryTab)
            {
                DrawHistory(session);
            }
            else if (tab == ShelvesTab)
            {
                DrawShelves(session);
            }
            else
            {
                NipaSetup.Draw(session);
            }
            EditorGUILayout.EndScrollView();

            if (tab == ChangesTab)
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
                {
                    NipaBranches.Load();
                    if (EditorGUILayout.DropdownButton(new GUIContent(NipaBranches.Label(), "Switch branch or tag"), FocusType.Passive, EditorStyles.toolbarDropDown, GUILayout.MinWidth(80)))
                        NipaBranches.ShowMenu();
                }
                else
                    GUILayout.Label(NipaHost.Connecting ? "Connecting..." : "Disconnected");
                GUILayout.FlexibleSpace();
                if (GUILayout.Button("Refresh", EditorStyles.toolbarButton))
                    NipaHost.Refresh();
                using (new EditorGUI.DisabledScope(session == null || NipaHost.Busy != null))
                {
                    if (GUILayout.Button("Update", EditorStyles.toolbarButton))
                        RunUpdate();
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
                DrawPending(cache);
            if (conflictCount > 0)
                EditorGUILayout.HelpBox(conflictCount + " file(s) in conflict. Resolve them in the Conflicts tab.", MessageType.Error);
            if (NipaHost.Error != null)
                EditorGUILayout.HelpBox(NipaHost.Error, MessageType.Warning);
            var maintenance = NipaHost.Maintenance;
            if (maintenance != null)
                EditorGUILayout.HelpBox(maintenance.Text, maintenance.Active ? MessageType.Warning : MessageType.Info);
            DrawActivity();
            if (warnings.Count > 0)
            {
                var text = string.Join("\n", warnings.Take(6).Select(w => "• " + w.Message));
                if (warnings.Count > 6)
                    text += "\n… and " + (warnings.Count - 6) + " more";
                EditorGUILayout.HelpBox(".meta warnings:\n" + text, MessageType.Warning);
            }
        }

        void DrawPending(StatusCache cache)
        {
            if (cache.Pending == "shelf")
            {
                EditorGUILayout.HelpBox("A shelf apply is waiting on conflicts. Resolve them in the Conflicts tab; the last resolution finishes it.", MessageType.Warning);
                using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
                {
                    if (GUILayout.Button("Abort shelf apply") && EditorUtility.DisplayDialog("Nipa: Abort shelf apply", "Put the applied files back to the pushed version? The shelf is kept.", "Abort", "Cancel")
                        && NipaHost.SaveOpenScenes())
                        NipaHost.Run("Abort shelf apply", (s, on) => s.AbortShelfApplyAsync(on), refreshAssets: true);
                }
                return;
            }
            var text = "A " + cache.Pending + " is waiting"
                + (cache.Pending == "revert" && cache.PendingRemaining > 1 ? " (" + cache.PendingRemaining + " steps left)" : string.Empty) + ".";
            EditorGUILayout.HelpBox(text, MessageType.Warning);
            using (new EditorGUILayout.HorizontalScope())
            using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
            {
                if (cache.Pending == "revert")
                {
                    using (new EditorGUI.DisabledScope(conflictCount > 0))
                    {
                        if (GUILayout.Button("Continue"))
                            RunRevert("continue");
                    }
                    if (GUILayout.Button("Skip"))
                        RunRevert("skip");
                    if (GUILayout.Button("Abort") && EditorUtility.DisplayDialog("Nipa: Abort revert", "Abort the revert and restore the original state?", "Abort", "Cancel"))
                        RunRevert("abort");
                }
                else
                {
                    using (new EditorGUI.DisabledScope(conflictCount > 0))
                    {
                        if (GUILayout.Button("Finish (push)"))
                            PushWithLockCheck(string.IsNullOrWhiteSpace(message) ? "Merge" : message);
                    }
                    if (GUILayout.Button("Abort") && EditorUtility.DisplayDialog("Nipa: Abort merge", "Abort the merge and restore the original state?", "Abort", "Cancel")
                        && NipaHost.SaveOpenScenes())
                        NipaHost.Run("Abort merge", (s, on) => s.AbortMergeAsync(on), refreshAssets: true);
                }
            }
        }

        static void RunUpdate()
        {
            NipaHost.Sync("Update", "update", null, null, (s, on) => s.UpdateAsync(on));
        }

        static void RunRevert(string step)
        {
            if (!NipaHost.SaveOpenScenes())
                return;
            NipaHost.Run("Revert", (s, on) => s.RevertStepAsync(step, on), refreshAssets: true);
        }

        void LoadConflicts(Core.Session.NipaSession session)
        {
            if (conflictsLoading)
                return;
            if (ReferenceEquals(conflictsSession, session) && conflictsVersion == session.Cache.Version)
                return;
            conflictsSession = session;
            conflictsVersion = session.Cache.Version;
            conflictsLoading = true;
            Task.Run(async () =>
            {
                ConflictsResponse set = null;
                try
                {
                    set = await session.ConflictsAsync().ConfigureAwait(false);
                }
                catch (System.Exception)
                {
                }
                MainThread.Post(() =>
                {
                    conflictsLoading = false;
                    if (set != null)
                        conflictSet = set;
                    Repaint();
                });
            });
        }

        void DrawConflicts(Core.Session.NipaSession session)
        {
            if (conflictSet == null || string.IsNullOrEmpty(conflictSet.Op) || conflictSet.Files.Count == 0)
            {
                EditorGUILayout.HelpBox("No conflicts.", MessageType.None);
                return;
            }
            var tool = MergeTool.ToolPath(EditorApplication.applicationContentsPath);
            var toolFound = System.IO.File.Exists(tool);
            EditorGUILayout.LabelField(conflictSet.Op + " conflicts (" + conflictSet.Files.Count(f => !f.Resolved) + " open)", EditorStyles.boldLabel);
            foreach (var f in conflictSet.Files)
            {
                var action = MergeTool.Classify(f.Kind, f.Binary, f.Resolved, f.Path);
                using (new EditorGUILayout.HorizontalScope())
                {
                    GUILayout.Label(new GUIContent(f.Path + "  [" + (f.Resolved ? "resolved" : f.Kind) + "]", f.Path), GUILayout.MinWidth(80));
                    GUILayout.FlexibleSpace();
                    using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
                    {
                        if (action == ConflictAction.MergeTool)
                        {
                            using (new EditorGUI.DisabledScope(!toolFound))
                            {
                                if (GUILayout.Button(new GUIContent("Merge", toolFound ? "Run UnityYAMLMerge" : "UnityYAMLMerge not found"), GUILayout.Width(56)))
                                    RunMergeTool(f.Path, tool);
                            }
                        }
                        if (f.Viewer == "image" && GUILayout.Button(new GUIContent("Compare", "Show mine and theirs side by side"), GUILayout.Width(64)))
                            CompareConflict(f.Path);
                        if (GUILayout.Button("Mine", GUILayout.Width(48)))
                            Resolve(f.Path, "ours");
                        if (GUILayout.Button("Theirs", GUILayout.Width(56)))
                            Resolve(f.Path, "theirs");
                    }
                }
            }
        }

        void CompareConflict(string path)
        {
            var sides = Core.Session.NipaSession.Sides(conflictSet, path);
            var mine = string.IsNullOrEmpty(conflictSet.Ours?.Label) ? "Mine" : "Mine (" + conflictSet.Ours.Label + ")";
            var theirs = string.IsNullOrEmpty(conflictSet.Theirs?.Label) ? "Theirs" : "Theirs (" + conflictSet.Theirs.Label + ")";
            NipaImageCompare.Open(path, sides.Ours, mine, sides.Theirs, theirs);
        }

        static void Resolve(string path, string choice)
        {
            NipaHost.Run("Resolve", (s, _) => s.ResolveAsync(path, choice), refreshAssets: true);
        }

        void RunMergeTool(string path, string tool)
        {
            var set = conflictSet;
            var work = System.IO.Path.Combine(System.IO.Path.GetTempPath(), "nipa-merge-" + System.Guid.NewGuid().ToString("N"));
            var merged = false;
            NipaHost.Run("Merge " + System.IO.Path.GetFileName(path), async (s, _) =>
            {
                try
                {
                    merged = await s.MergeToolResolveAsync(set, path, tool, new SystemMergeProcess(), work);
                }
                finally
                {
                    if (System.IO.Directory.Exists(work))
                        System.IO.Directory.Delete(work, true);
                }
            }, refreshAssets: true, onSuccess: () =>
            {
                if (!merged)
                    EditorUtility.DisplayDialog("Nipa: Merge", "UnityYAMLMerge could not merge " + path + " cleanly. Pick Mine or Theirs, or resolve it in Nipa Desktop.", "OK");
            });
        }

        void DrawHistory(Core.Session.NipaSession session)
        {
            var selected = NipaHost.SelectedClonePaths().FirstOrDefault();
            var key = session.Root + "|" + session.Cache.Branch + "|" + session.Cache.Head?.Name + "|" + session.Cache.Version;
            if (selected != historyPath || key != historyKey)
            {
                historyPath = selected;
                historyKey = key;
                LoadHistory(session, selected, key);
            }
            EditorGUILayout.LabelField(string.IsNullOrEmpty(historyPath) ? "Project history" : "History of " + historyPath, EditorStyles.boldLabel);
            foreach (var c in history)
            {
                using (new EditorGUILayout.HorizontalScope())
                {
                    var first = (c.Message ?? string.Empty).Split('\n')[0];
                    GUILayout.Label(new GUIContent(first, c.Message), GUILayout.MinWidth(80));
                    GUILayout.FlexibleSpace();
                    GUILayout.Label(c.AuthorName, EditorStyles.miniLabel, GUILayout.Width(90));
                    var link = session.DesktopLink("commits/" + c.CommitId);
                    using (new EditorGUI.DisabledScope(link == null))
                    {
                        if (GUILayout.Button("Diff", GUILayout.Width(40)))
                            Application.OpenURL(link);
                    }
                }
            }
            if (historyError != null)
                EditorGUILayout.HelpBox("Could not load history: " + historyError, MessageType.Warning);
            else if (history.Count == 0)
                EditorGUILayout.HelpBox(historyLoading ? "Loading..." : "No commits.", MessageType.None);
            if (historyNext != null && !historyLoading)
            {
                using (new EditorGUI.DisabledScope(historyMoreLoading))
                {
                    if (GUILayout.Button(historyMoreLoading ? "Loading..." : "Load more"))
                        LoadHistory(session, historyPath, historyKey, historyNext);
                }
            }
        }

        void LoadHistory(Core.Session.NipaSession session, string path, string key, string start = null)
        {
            var more = start != null;
            if (more)
            {
                historyMoreLoading = true;
            }
            else
            {
                historyLoading = true;
                historyMoreLoading = false;
                historyNext = null;
                history = new List<CommitLogEntry>();
            }
            historyError = null;
            Task.Run(async () =>
            {
                HistoryPage page = null;
                string error = null;
                try
                {
                    page = await session.HistoryPageAsync(path, start).ConfigureAwait(false);
                }
                catch (System.Exception e)
                {
                    error = MaintenanceNotes.ErrorText(e);
                }
                MainThread.Post(() =>
                {
                    if (key != historyKey || path != historyPath)
                        return;
                    historyLoading = false;
                    historyMoreLoading = false;
                    historyError = error;
                    if (page != null)
                    {
                        history = more ? history.Concat(page.Commits).ToList() : page.Commits;
                        historyNext = page.Next;
                    }
                    Repaint();
                });
            });
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
                        RunUpdate();
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
                    var requests = l.Requests ?? new List<FileLockRequestDetail>();
                    if (policy.IsMine(l) && requests.Count > 0)
                    {
                        var who = string.Join("\n", requests.Select(r => (string.IsNullOrEmpty(r.RequesterName) ? r.RequesterId : r.RequesterName)
                            + (string.IsNullOrWhiteSpace(r.Message) ? string.Empty : ": " + r.Message)));
                        GUILayout.Label(new GUIContent(requests.Count + " request(s)", "Asked to release:\n" + who), EditorStyles.miniBoldLabel, GUILayout.Width(80));
                    }
                    var expiry = NipaLocks.Expiry(l);
                    if (expiry != null)
                        GUILayout.Label(new GUIContent(expiry, "Expires " + l.ExpiresAt.Value.ToLocalTime().ToString("yyyy-MM-dd HH:mm")), EditorStyles.miniLabel, GUILayout.Width(90));
                    GUILayout.Label(policy.IsMine(l) ? "you" : l.HeldByName, EditorStyles.miniLabel, GUILayout.Width(80));
                    using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
                    {
                        if (!policy.IsMine(l))
                        {
                            var target = ProjectPaths.AssetOf(ProjectPaths.Normalize(l.Path).Trim('/'));
                            if (GUILayout.Button(new GUIContent("Ask", "Ask the holder to release this lock"), GUILayout.Width(40)))
                                NipaLocks.RequestRelease(target);
                            var watched = NipaLocks.IsWatched(target);
                            if (GUILayout.Button(new GUIContent(watched ? "Unwatch" : "Notify", watched ? "Stop the notification" : "Ask to lock it when it is released"), GUILayout.Width(60)))
                                NipaLocks.ToggleWatch(target);
                        }
                        if (policy.IsMine(l) && l.ExpiresAt != null && GUILayout.Button(new GUIContent("Renew", "Extend the lock's expiry"), GUILayout.Width(52)))
                            NipaLocks.Renew(l);
                        using (new EditorGUI.DisabledScope(!policy.CanRelease(l)))
                        {
                            if (GUILayout.Button("Release", GUILayout.Width(64)))
                                NipaHost.Run("Release", (s, _) => s.ReleaseAsync(l));
                        }
                    }
                }
            }
            if (locks.Count == 0)
                EditorGUILayout.HelpBox("No locks.", MessageType.None);
            var watches = NipaHost.Watches.Paths;
            if (watches.Count == 0)
                return;
            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Notify when free (" + watches.Count + ")", EditorStyles.boldLabel);
            foreach (var w in watches.ToList())
            {
                using (new EditorGUILayout.HorizontalScope())
                {
                    GUILayout.Label(new GUIContent(w, w), GUILayout.MinWidth(80));
                    GUILayout.FlexibleSpace();
                    if (GUILayout.Button("Remove", GUILayout.Width(64)))
                        NipaHost.Watches.Unwatch(w);
                }
            }
        }

        void PushWithLockCheck(string msg, bool skipChecks = false)
        {
            if (!NipaHost.Allowed("Push", false))
            {
                message = msg;
                return;
            }
            List<string> needed = null;
            List<string> checks = null;
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
                    needed = check.Needed;
                    return;
                }
                if (!skipChecks)
                {
                    checks = await PlanWarnings(s);
                    if (checks.Count > 0)
                        return;
                }
                await s.PushAsync(msg, on);
            }, onSuccess: () =>
            {
                if (needed != null)
                    PromptLock(needed, msg);
                else if (checks != null && checks.Count > 0)
                    PromptChecks(checks, msg);
            });
        }

        static async Task<List<string>> PlanWarnings(Core.Session.NipaSession s)
        {
            try
            {
                return PushChecks.Warnings(await s.PushPlanAsync());
            }
            catch (DaemonException e) when (e.Maintenance != null)
            {
                throw;
            }
            catch (System.Exception e)
            {
                Debug.LogWarning("[Nipa] Push checks failed: " + e.Message);
                return new List<string>();
            }
        }

        void PromptChecks(List<string> checks, string msg)
        {
            var list = string.Join("\n", checks.Take(12).Select(c => "• " + c)) + (checks.Count > 12 ? "\n… and " + (checks.Count - 12) + " more" : string.Empty);
            foreach (var c in checks)
                Debug.LogWarning("[Nipa] Push check: " + c);
            if (EditorUtility.DisplayDialog("Nipa: push checks", "The push checks found " + checks.Count + " problem(s):\n" + list
                + "\n\nThe full list is in the Console.", "Push anyway", "Cancel"))
                PushWithLockCheck(msg, true);
            else
                message = msg;
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
                    if (GUILayout.Button(new GUIContent("Shelve", "Put the selected changes aside and restore the files")))
                        Shelve(selected.ToList());
                }
                if (GUILayout.Button(allLabel))
                    run(rows.Select(r => r.Path).ToList());
            }
        }

        void Shelve(List<string> paths)
        {
            if (paths.Count == 0)
                return;
            NipaPrompt.Ask("Nipa: Shelve", "Shelve " + paths.Count + " change(s). The files go back to the pushed version; apply the shelf later from the Shelves tab.",
                OpSummary.ShelfName("shelve", System.DateTime.Now).Substring("Before ".Length), "Shelve", false, name =>
                {
                    if (!NipaHost.SaveOpenScenes())
                        return;
                    OpResult result = null;
                    NipaHost.Run("Shelve", async (s, on) => result = await s.ShelveAsync(name, paths, false, on), refreshAssets: true, onSuccess: () =>
                    {
                        shelvesVersion = -1;
                        NipaHost.Notices.Add(OpSummary.Describe(result) ?? "shelved");
                        NipaHost.NotifyChanged();
                    });
                });
        }

        void LoadShelves(Core.Session.NipaSession session)
        {
            if (shelvesLoading)
                return;
            if (ReferenceEquals(shelvesSession, session) && shelvesVersion == session.Cache.Version)
                return;
            shelvesSession = session;
            shelvesVersion = session.Cache.Version;
            shelvesLoading = true;
            Task.Run(async () =>
            {
                ShelveListResponse list = null;
                string error = null;
                try
                {
                    list = await session.ShelvesAsync().ConfigureAwait(false);
                }
                catch (System.Exception e)
                {
                    error = e.Message;
                }
                MainThread.Post(() =>
                {
                    shelvesLoading = false;
                    shelvesError = error;
                    if (list != null)
                        shelves = list;
                    Repaint();
                });
            });
        }

        void DrawShelves(Core.Session.NipaSession session)
        {
            if (shelvesError != null)
                EditorGUILayout.HelpBox("Could not load shelves: " + shelvesError, MessageType.Warning);
            if (shelves == null || shelves.Shelves.Count == 0)
            {
                EditorGUILayout.HelpBox(shelvesLoading ? "Loading..." : "No shelves. Select changes and press Shelve to put them aside.", MessageType.None);
                return;
            }
            var pending = !string.IsNullOrEmpty(session.Cache.Pending);
            foreach (var shelf in shelves.Shelves.OrderByDescending(x => x.CreatedAt))
            {
                var entries = shelf.Entries ?? new List<ShelfEntry>();
                var when = System.DateTimeOffset.FromUnixTimeSeconds(shelf.CreatedAt).ToLocalTime().ToString("yyyy-MM-dd HH:mm");
                var scope = string.IsNullOrEmpty(shelf.Tag) ? shelf.Branch : "tag " + shelf.Tag;
                var tip = string.Join("\n", entries.Take(20).Select(e => e.Status + "  " + e.Path)) + (entries.Count > 20 ? "\n…" : string.Empty);
                using (new EditorGUILayout.HorizontalScope())
                {
                    var applying = shelf.Id == shelves.Applying;
                    GUILayout.Label(new GUIContent(shelf.Name + (applying ? "  [applying]" : string.Empty), tip), GUILayout.MinWidth(80));
                    GUILayout.FlexibleSpace();
                    GUILayout.Label(entries.Count + " file(s) · " + scope + " · " + when, EditorStyles.miniLabel);
                    using (new EditorGUI.DisabledScope(NipaHost.Busy != null || pending))
                    {
                        if (GUILayout.Button(new GUIContent("Apply", "Apply and delete the shelf"), GUILayout.Width(48)))
                            ApplyShelf(shelf, false);
                        if (GUILayout.Button(new GUIContent("Keep", "Apply and keep the shelf"), GUILayout.Width(44)))
                            ApplyShelf(shelf, true);
                        if (GUILayout.Button("Delete", GUILayout.Width(52))
                            && EditorUtility.DisplayDialog("Nipa: Delete shelf", "Delete shelf \"" + shelf.Name + "\"? Its changes are lost.", "Delete", "Cancel"))
                        {
                            var id = shelf.Id;
                            NipaHost.Run("Delete shelf", (s, _) => s.DropShelfAsync(id), onSuccess: () => shelvesVersion = -1);
                        }
                    }
                }
            }
        }

        void ApplyShelf(Shelf shelf, bool keep)
        {
            if (!NipaHost.SaveOpenScenes())
                return;
            var id = shelf.Id;
            OpResult result = null;
            NipaHost.Run("Apply shelf", async (s, on) => result = await s.ApplyShelfAsync(id, keep, on), refreshAssets: true, onSuccess: () =>
            {
                shelvesVersion = -1;
                if (OpSummary.HasConflicts(result))
                    tab = ConflictsTab;
                NipaHost.Notices.Add("\"" + shelf.Name + "\": " + OpSummary.Describe(result));
                NipaHost.NotifyChanged();
            });
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
            var paused = NipaHost.Maintenance?.BlocksWrites == true;
            using (new EditorGUI.DisabledScope(stagedCount == 0 || string.IsNullOrWhiteSpace(message) || blocked || paused))
            {
                if (GUILayout.Button(new GUIContent("Push", paused ? "The server is in maintenance" : null)))
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
