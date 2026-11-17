using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using UnityEditor;

namespace Nipa.Editor
{
    internal static class NipaBranches
    {
        static List<Branch> branches = new List<Branch>();
        static List<Tag> tags = new List<Tag>();
        static bool loading;
        static object loadedFor;
        static DateTime failedAt = DateTime.MinValue;
        static readonly TimeSpan RetryAfter = TimeSpan.FromSeconds(30);

        public static string Label()
        {
            var cache = NipaHost.Session?.Cache;
            if (cache == null)
                return "Nipa";
            return cache.Head != null ? "tag " + cache.Head.Name : cache.Branch ?? "?";
        }

        public static void Load(bool force = false)
        {
            var session = NipaHost.Session;
            if (session == null || loading || (!force && ReferenceEquals(loadedFor, session)))
                return;
            if (!force && DateTime.UtcNow - failedAt < RetryAfter)
                return;
            loading = true;
            Task.Run(async () =>
            {
                List<Branch> b = null;
                List<Tag> t = null;
                try
                {
                    b = await session.BranchesAsync().ConfigureAwait(false);
                    t = await session.TagsAsync().ConfigureAwait(false);
                }
                catch (Exception)
                {
                }
                MainThread.Post(() =>
                {
                    loading = false;
                    if (b == null)
                    {
                        failedAt = DateTime.UtcNow;
                        return;
                    }
                    failedAt = DateTime.MinValue;
                    branches = b;
                    tags = t ?? new List<Tag>();
                    loadedFor = session;
                    NipaHost.NotifyChanged();
                });
            });
        }

        public static void ShowMenu()
        {
            Load();
            var session = NipaHost.Session;
            var menu = new GenericMenu();
            if (session == null)
            {
                menu.AddDisabledItem(new UnityEngine.GUIContent("Not connected"));
                menu.ShowAsContext();
                return;
            }
            var current = session.Cache.Head == null ? session.Cache.Branch : null;
            foreach (var b in branches.OrderBy(b => b.Name, StringComparer.Ordinal))
            {
                var name = b.Name;
                menu.AddItem(new UnityEngine.GUIContent("Branches/" + name), name == current, () => Switch(name, null));
            }
            foreach (var t in tags.OrderBy(t => t.Name, StringComparer.Ordinal))
            {
                var name = t.Name;
                menu.AddItem(new UnityEngine.GUIContent("Tags/" + name), session.Cache.Head?.Name == name, () => Switch(null, name));
            }
            if (branches.Count == 0 && tags.Count == 0)
                menu.AddDisabledItem(new UnityEngine.GUIContent(loading ? "Loading..." : "No branches"));
            menu.AddSeparator(string.Empty);
            var attached = current != null && string.IsNullOrEmpty(session.Cache.Pending);
            foreach (var b in branches.Where(b => b.Name != current).OrderBy(b => b.Name, StringComparer.Ordinal))
            {
                var name = b.Name;
                var label = new UnityEngine.GUIContent("Merge into current branch/" + name);
                if (attached)
                    menu.AddItem(label, false, () => Merge(name));
                else
                    menu.AddDisabledItem(label);
            }
            menu.AddItem(new UnityEngine.GUIContent("New branch..."), false, NewBranch);
            foreach (var b in branches.Where(b => !b.IsDefault && b.Name != current).OrderBy(b => b.Name, StringComparer.Ordinal))
            {
                var name = b.Name;
                menu.AddItem(new UnityEngine.GUIContent("Delete branch/" + name), false, () => Delete(name));
            }
            menu.AddSeparator(string.Empty);
            menu.AddItem(new UnityEngine.GUIContent("Reload list"), false, () => Load(true));
            menu.ShowAsContext();
        }

        static bool Blocked(Core.Session.NipaSession session, string title)
        {
            if (string.IsNullOrEmpty(session.Cache.Pending))
                return false;
            EditorUtility.DisplayDialog("Nipa: " + title, "A " + session.Cache.Pending + " is waiting. Finish or abort it first.", "OK");
            return true;
        }

        public static void NewBranch()
        {
            var session = NipaHost.Session;
            if (session == null)
                return;
            var from = session.Cache.Head != null ? "tag " + session.Cache.Head.Name : session.Cache.Branch;
            NipaPrompt.Ask("Nipa: New branch", "Create a branch from " + from + " and switch to it.", string.Empty, "Create", false, name =>
                NipaHost.Run("New branch", (s, _) => s.CreateBranchAsync(name), onSuccess: () =>
                {
                    Load(true);
                    Switch(name, null);
                }));
        }

        public static void Delete(string name)
        {
            if (!EditorUtility.DisplayDialog("Nipa: Delete branch", "Delete branch " + name + " on the server?", "Delete", "Cancel"))
                return;
            NipaHost.Run("Delete branch", (s, _) => s.DeleteBranchAsync(name), onSuccess: () => Load(true));
        }

        public static void Merge(string source)
        {
            var session = NipaHost.Session;
            if (session == null || NipaHost.Busy != null || Blocked(session, "Merge"))
                return;
            var target = session.Cache.Branch;
            if (!EditorUtility.DisplayDialog("Nipa: Merge", "Merge " + source + " into " + target + "?", "Merge", "Cancel"))
                return;
            if (!NipaHost.SaveOpenScenes())
                return;
            OpResult result = null;
            NipaHost.Run("Merge", async (s, on) => result = await s.MergeAsync(source, on), refreshAssets: true, onSuccess: () =>
            {
                if (Core.Session.OpSummary.HasConflicts(result))
                    NipaWindow.OpenTab(NipaWindow.ConflictsTab);
                EditorUtility.DisplayDialog("Nipa: Merge", "Merge " + source + " into " + target + ": " + Core.Session.OpSummary.Describe(result) + ".", "OK");
            });
        }

        public static void Switch(string branch, string tag)
        {
            var session = NipaHost.Session;
            if (session == null || NipaHost.Busy != null)
                return;
            if (Blocked(session, "Switch"))
                return;
            var target = tag != null ? "tag " + tag : branch;
            NipaHost.Sync("Switch to " + target, "switch to " + target, branch, tag, (s, on) => s.SwitchAsync(branch, tag, on));
        }
    }
}
