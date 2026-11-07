using System;
using System.Collections.Generic;
using System.Linq;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Paths;

namespace Nipa.Editor.Core.Locks
{
    public enum LockState
    {
        Free,
        Mine,
        Theirs,
    }

    public sealed class LockInfo
    {
        public static readonly LockInfo Free = new LockInfo(LockState.Free, null, null);

        public LockState State { get; }
        public string Holder { get; }
        public string LockPath { get; }

        public LockInfo(LockState state, string holder, string lockPath)
        {
            State = state;
            Holder = holder;
            LockPath = lockPath;
        }
    }

    public sealed class PushCheck
    {
        public List<string> Needed { get; } = new List<string>();
        public List<FileLockState> Theirs { get; } = new List<FileLockState>();
        public bool Blocked => Needed.Count > 0 || Theirs.Count > 0;
    }

    public sealed class LockPolicy
    {
        public static readonly string[] DefaultExtensions =
        {
            ".unity", ".prefab", ".fbx", ".blend", ".psd", ".png", ".jpg", ".tga", ".exr", ".wav", ".mp3", ".ogg",
            ".mp4", ".mov", ".anim", ".controller", ".asset", ".mat", ".ttf", ".otf", ".dll", ".so", ".a",
        };

        public static readonly LockPolicy Empty = new LockPolicy(null, false, null);

        readonly HashSet<string> strictExtensions;
        readonly List<FileLockDetail> scoped;

        public string UserId { get; }
        public string Branch { get; }
        public bool DefaultBranch { get; }
        public bool CanReleaseAny { get; }
        public bool Strict { get; }
        public IReadOnlyList<FileLockDetail> Locks { get; }
        public IReadOnlyList<FileLockState> Files { get; }

        public LockPolicy(LockStatusResponse status, bool strict, IEnumerable<string> strictExtensions)
        {
            UserId = status?.UserId ?? string.Empty;
            Branch = status?.Branch ?? string.Empty;
            DefaultBranch = status?.DefaultBranch ?? false;
            CanReleaseAny = status?.CanReleaseAny ?? false;
            Strict = strict;
            Locks = (status?.Locks ?? new List<FileLockDetail>()).AsReadOnly();
            Files = (status?.Files ?? new List<FileLockState>()).AsReadOnly();
            this.strictExtensions = new HashSet<string>(
                (strictExtensions ?? DefaultExtensions).Select(NormalizeExtension).Where(e => e.Length > 0),
                StringComparer.OrdinalIgnoreCase);
            scoped = Locks.Where(InScope).ToList();
        }

        public string LockBranch => DefaultBranch ? string.Empty : Branch;

        public static bool Covers(string lockPath, string path)
        {
            var l = Trim(lockPath);
            var p = Trim(path);
            if (l.Length == 0)
                return false;
            return p == l || p.StartsWith(l + "/", StringComparison.Ordinal);
        }

        public bool InScope(FileLockDetail l)
        {
            return l.Global ? DefaultBranch : string.Equals(l.Branch, Branch, StringComparison.Ordinal);
        }

        public bool IsMine(FileLockDetail l)
        {
            return UserId.Length > 0 && string.Equals(l.HeldBy, UserId, StringComparison.Ordinal);
        }

        public bool CanRelease(FileLockDetail l)
        {
            return IsMine(l) || CanReleaseAny;
        }

        public IEnumerable<FileLockDetail> Mine => scoped.Where(IsMine);

        public LockInfo Lookup(string clonePath)
        {
            LockInfo mine = null;
            foreach (var p in ProjectPaths.Pair(Trim(clonePath)))
            {
                foreach (var l in scoped)
                {
                    if (!Covers(l.Path, p))
                        continue;
                    var holder = string.IsNullOrEmpty(l.HeldByName) ? l.HeldBy : l.HeldByName;
                    if (!IsMine(l))
                        return new LockInfo(LockState.Theirs, holder, Trim(l.Path));
                    mine = mine ?? new LockInfo(LockState.Mine, holder, Trim(l.Path));
                }
            }
            return mine ?? LockInfo.Free;
        }

        public bool HeldByMe(string clonePath)
        {
            return scoped.Any(l => IsMine(l) && Covers(l.Path, clonePath));
        }

        public bool RequiresLock(string clonePath)
        {
            return Strict && strictExtensions.Contains(Extension(ProjectPaths.AssetOf(Trim(clonePath))));
        }

        public bool IsOpenForEdit(string clonePath, bool isNew, out string message)
        {
            var info = Lookup(clonePath);
            message = null;
            if (info.State == LockState.Theirs)
            {
                message = Trim(clonePath) + " is locked by " + info.Holder;
                return false;
            }
            if (!isNew && info.State == LockState.Free && RequiresLock(clonePath))
            {
                message = Trim(clonePath) + " must be locked before editing";
                return false;
            }
            return true;
        }

        public bool CanChange(string clonePath, out string message)
        {
            var info = Lookup(clonePath);
            var p = Trim(clonePath);
            message = null;
            if (info.State == LockState.Theirs)
            {
                message = p + " is locked by " + info.Holder;
                return false;
            }
            if (p.Length == 0)
                return true;
            foreach (var l in scoped)
            {
                if (IsMine(l) || Trim(l.Path) == p || !Covers(p, l.Path))
                    continue;
                message = Trim(l.Path) + " inside " + p + " is locked by " + (string.IsNullOrEmpty(l.HeldByName) ? l.HeldBy : l.HeldByName);
                return false;
            }
            return true;
        }

        public PushCheck CheckPush()
        {
            var check = new PushCheck();
            var needed = new HashSet<string>();
            foreach (var f in Files)
            {
                if (f.State == "needed" && needed.Add(Trim(f.Path)))
                    check.Needed.Add(Trim(f.Path));
                else if (f.State == "theirs")
                    check.Theirs.Add(f);
            }
            return check;
        }

        static string NormalizeExtension(string e)
        {
            e = (e ?? string.Empty).Trim();
            if (e.Length == 0)
                return e;
            return e[0] == '.' ? e : "." + e;
        }

        static string Extension(string path)
        {
            var slash = path.LastIndexOf('/');
            var dot = path.LastIndexOf('.');
            return dot > slash + 1 ? path.Substring(dot) : string.Empty;
        }

        static string Trim(string p)
        {
            return ProjectPaths.Normalize(p).Trim('/');
        }
    }
}
