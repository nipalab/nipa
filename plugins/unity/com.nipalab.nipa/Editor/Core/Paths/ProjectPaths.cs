using System;
using System.Collections.Generic;
using System.IO;

namespace Nipa.Editor.Core.Paths
{
    public sealed class ProjectPaths
    {
        const int MaxParents = 32;

        public string ProjectDir { get; }
        public string CloneRoot { get; }
        public string Prefix { get; }

        public ProjectPaths(string projectDir, string cloneRoot)
        {
            ProjectDir = Full(projectDir);
            CloneRoot = Full(cloneRoot);
            var rel = Normalize(RelativePath(CloneRoot, ProjectDir));
            Prefix = rel == "." ? string.Empty : rel;
        }

        public static ProjectPaths Discover(string projectDir)
        {
            var dir = new DirectoryInfo(Full(projectDir));
            for (var i = 0; i < MaxParents && dir != null; i++, dir = dir.Parent)
            {
                if (File.Exists(Path.Combine(dir.FullName, ".nipa", "config")))
                    return new ProjectPaths(projectDir, dir.FullName);
            }
            return null;
        }

        public string ToClonePath(string unityPath)
        {
            var p = Normalize(unityPath).TrimStart('/');
            if (Prefix.Length == 0)
                return p;
            return p.Length == 0 ? Prefix : Prefix + "/" + p;
        }

        public string ToUnityPath(string clonePath)
        {
            var p = Normalize(clonePath).TrimStart('/');
            if (Prefix.Length == 0)
                return p;
            if (p == Prefix)
                return string.Empty;
            return p.StartsWith(Prefix + "/", StringComparison.Ordinal) ? p.Substring(Prefix.Length + 1) : null;
        }

        public bool IsInProject(string clonePath)
        {
            return ToUnityPath(clonePath) != null;
        }

        public static bool IsMeta(string path)
        {
            return path.EndsWith(".meta", StringComparison.Ordinal);
        }

        public static string MetaOf(string path)
        {
            return IsMeta(path) ? path : path + ".meta";
        }

        public static string AssetOf(string path)
        {
            return IsMeta(path) ? path.Substring(0, path.Length - ".meta".Length) : path;
        }

        public static string Counterpart(string path)
        {
            return IsMeta(path) ? AssetOf(path) : MetaOf(path);
        }

        public static IEnumerable<string> Pair(string path)
        {
            yield return AssetOf(path);
            yield return MetaOf(path);
        }

        public static string Normalize(string path)
        {
            return (path ?? string.Empty).Replace('\\', '/');
        }

        static string Full(string path)
        {
            var full = Path.GetFullPath(path);
            return full == Path.GetPathRoot(full)
                ? full
                : full.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        }

        static string RelativePath(string from, string to)
        {
            if (string.Equals(from, to, StringComparison.Ordinal))
                return ".";
            var sep = Path.DirectorySeparatorChar;
            var prefix = from.EndsWith(sep.ToString()) ? from : from + sep;
            if (!to.StartsWith(prefix, StringComparison.Ordinal))
                throw new ArgumentException("project " + to + " is not inside clone " + from);
            return to.Substring(prefix.Length);
        }
    }
}
