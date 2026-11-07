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
            menu.AddItem(new UnityEngine.GUIContent("Reload list"), false, () => Load(true));
            menu.ShowAsContext();
        }

        public static void Switch(string branch, string tag)
        {
            var session = NipaHost.Session;
            if (session == null || NipaHost.Busy != null)
                return;
            if (!string.IsNullOrEmpty(session.Cache.Pending))
            {
                EditorUtility.DisplayDialog("Nipa: Switch", "A " + session.Cache.Pending + " is waiting. Finish or abort it before switching.", "OK");
                return;
            }
            var target = tag != null ? "tag " + tag : branch;
            var dirty = session.Cache.Snapshot().Count(kv => (kv.Value & Core.Status.FileState.Untracked) == 0);
            var warning = dirty > 0 ? "\n\nYou have " + dirty + " uncommitted path(s); the switch can overwrite them." : string.Empty;
            if (!EditorUtility.DisplayDialog("Nipa: Switch", "Switch the working copy to " + target + "?" + warning, "Switch", "Cancel"))
                return;
            NipaHost.Run("Switch", (s, on) => s.SwitchAsync(branch, tag, on), refreshAssets: true);
        }
    }
}
