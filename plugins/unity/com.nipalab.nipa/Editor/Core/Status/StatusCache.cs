using System;
using System.Collections.Generic;
using Nipa.Editor.Core.Daemon;

namespace Nipa.Editor.Core.Status
{
    [Flags]
    public enum FileState
    {
        None = 0,
        Staged = 1,
        Added = 2,
        Modified = 4,
        Deleted = 8,
        Untracked = 16,
        Missing = 32,
        Conflict = 64,
        Resolved = 128,
    }

    public sealed class StatusCache
    {
        readonly object gate = new object();
        Dictionary<string, FileState> files = new Dictionary<string, FileState>();
        Dictionary<string, FileState> dirs = new Dictionary<string, FileState>();

        public string Branch { get; private set; }
        public StatusHead Head { get; private set; }
        public string Pending { get; private set; }
        public int PendingRemaining { get; private set; }
        public int Version { get; private set; }

        public event Action<IReadOnlyCollection<string>> Changed;

        public FileState Get(string path)
        {
            path = Trim(path);
            lock (gate)
            {
                if (files.TryGetValue(path, out var s))
                    return s;
                return dirs.TryGetValue(path, out s) ? s : FileState.None;
            }
        }

        public bool Has(string path)
        {
            lock (gate)
                return files.ContainsKey(Trim(path));
        }

        public IReadOnlyDictionary<string, FileState> Snapshot()
        {
            lock (gate)
                return new Dictionary<string, FileState>(files);
        }

        public IReadOnlyCollection<string> Apply(StatusResponse status)
        {
            var next = new Dictionary<string, FileState>();
            Add(next, status.Staged, FileState.Staged);
            Add(next, status.Added, FileState.Added);
            Add(next, status.Modified, FileState.Modified);
            Add(next, status.Deleted, FileState.Deleted | FileState.Staged);
            Add(next, status.Untracked, FileState.Untracked);
            Add(next, status.Missing, FileState.Missing);
            Add(next, status.Conflicts, FileState.Conflict);
            Add(next, status.Resolved, FileState.Resolved);
            var nextDirs = BuildDirs(next);

            var changed = new HashSet<string>();
            lock (gate)
            {
                Diff(files, next, changed);
                Diff(dirs, nextDirs, changed);
                files = next;
                dirs = nextDirs;
                if (Branch != status.Branch || Pending != status.Pending || PendingRemaining != status.PendingRemaining
                    || HeadKey(Head) != HeadKey(status.Head))
                    changed.Add(string.Empty);
                Branch = status.Branch;
                Head = status.Head;
                Pending = status.Pending;
                PendingRemaining = status.PendingRemaining;
                if (changed.Count > 0)
                    Version++;
            }
            if (changed.Count > 0)
                Changed?.Invoke(changed);
            return changed;
        }

        static string HeadKey(StatusHead h)
        {
            return h == null ? string.Empty : h.Kind + ":" + h.Name;
        }

        static void Add(Dictionary<string, FileState> into, List<string> paths, FileState state)
        {
            if (paths == null)
                return;
            foreach (var raw in paths)
            {
                var p = Trim(raw);
                into.TryGetValue(p, out var cur);
                into[p] = cur | state;
            }
        }

        static Dictionary<string, FileState> BuildDirs(Dictionary<string, FileState> files)
        {
            var dirs = new Dictionary<string, FileState>();
            foreach (var kv in files)
            {
                var p = kv.Key;
                var cut = p.LastIndexOf('/');
                while (cut > 0)
                {
                    p = p.Substring(0, cut);
                    dirs.TryGetValue(p, out var cur);
                    var merged = cur | kv.Value;
                    if (merged == cur && dirs.ContainsKey(p))
                        break;
                    dirs[p] = merged;
                    cut = p.LastIndexOf('/');
                }
            }
            return dirs;
        }

        static void Diff(Dictionary<string, FileState> old, Dictionary<string, FileState> next, HashSet<string> changed)
        {
            foreach (var kv in old)
            {
                if (!next.TryGetValue(kv.Key, out var s) || s != kv.Value)
                    changed.Add(kv.Key);
            }
            foreach (var kv in next)
            {
                if (!old.ContainsKey(kv.Key))
                    changed.Add(kv.Key);
            }
        }

        static string Trim(string p)
        {
            return (p ?? string.Empty).Replace('\\', '/').Trim('/');
        }
    }
}
