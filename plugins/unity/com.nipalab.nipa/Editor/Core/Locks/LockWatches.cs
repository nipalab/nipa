using System;
using System.Collections.Generic;
using System.Linq;
using Nipa.Editor.Core.Paths;

namespace Nipa.Editor.Core.Locks
{
    public sealed class LockWatches
    {
        readonly List<string> paths = new List<string>();

        public event Action Changed;

        public IReadOnlyList<string> Paths => paths;

        public static LockWatches Parse(string stored)
        {
            var w = new LockWatches();
            foreach (var line in (stored ?? string.Empty).Split('\n'))
            {
                var p = Key(line);
                if (p.Length > 0 && !w.paths.Contains(p))
                    w.paths.Add(p);
            }
            return w;
        }

        public string Serialize() => string.Join("\n", paths);

        public bool IsWatched(string clonePath) => paths.Contains(Key(clonePath));

        public void Watch(string clonePath)
        {
            var p = Key(clonePath);
            if (p.Length == 0 || paths.Contains(p))
                return;
            paths.Add(p);
            Changed?.Invoke();
        }

        public void Unwatch(string clonePath)
        {
            if (paths.Remove(Key(clonePath)))
                Changed?.Invoke();
        }

        public static List<string> Hits(IEnumerable<string> watched, string releasedPath)
        {
            var released = ProjectPaths.Normalize(releasedPath ?? string.Empty).Trim('/');
            if (released.Length == 0)
                return new List<string>();
            var asset = ProjectPaths.AssetOf(released);
            return watched.Where(p => LockPolicy.Covers(released, p) || ProjectPaths.AssetOf(p) == asset).ToList();
        }

        public List<string> TakeHits(string releasedPath)
        {
            var found = Hits(paths, releasedPath);
            if (found.Count == 0)
                return found;
            foreach (var p in found)
                paths.Remove(p);
            Changed?.Invoke();
            return found;
        }

        static string Key(string clonePath)
        {
            return ProjectPaths.AssetOf(ProjectPaths.Normalize(clonePath ?? string.Empty).Trim().Trim('/'));
        }
    }
}
