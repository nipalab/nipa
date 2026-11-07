using System.Collections.Generic;
using System.Linq;
using UnityEditor;

namespace Nipa.Editor
{
    public class NipaAssetProcessor : AssetModificationProcessor
    {
        static bool IsOpenForEdit(string assetPath, out string message)
        {
            message = null;
            var session = NipaHost.Session;
            if (session == null || !NipaHost.IsManaged(assetPath))
                return true;
            return session.IsOpenForEdit(session.Paths.ToClonePath(assetPath), out message);
        }

        static bool IsOpenForEdit(string assetPath, out string message, StatusQueryOptions statusOptions)
        {
            return IsOpenForEdit(assetPath, out message);
        }

        static bool CanOpenForEdit(string[] paths, List<string> outNotEditablePaths, StatusQueryOptions statusQueryOptions)
        {
            var session = NipaHost.Session;
            if (session == null)
                return true;
            foreach (var p in paths.Where(NipaHost.IsManaged))
            {
                if (!session.Locks.CanChange(session.Paths.ToClonePath(p), out _))
                    outNotEditablePaths.Add(p);
            }
            return !paths.Any(outNotEditablePaths.Contains);
        }

        static bool MakeEditable(string[] paths)
        {
            return MakeEditable(paths, null, new List<string>());
        }

        static bool MakeEditable(string[] paths, string prompt, List<string> outNotEditablePaths)
        {
            var session = NipaHost.Session;
            if (session == null)
                return true;
            var managed = paths.Where(NipaHost.IsManaged).ToList();
            if (managed.Count == 0)
                return true;
            var blocked = managed.Where(p => !session.Locks.CanChange(session.Paths.ToClonePath(p), out _)).ToList();
            if (blocked.Count > 0)
            {
                outNotEditablePaths?.AddRange(blocked);
                NipaLocks.Tell("Nipa: Lock", string.Join("\n", blocked.Select(p =>
                {
                    session.Locks.CanChange(session.Paths.ToClonePath(p), out var message);
                    return message;
                })));
                return false;
            }
            var needLock = managed.Where(p => !session.IsOpenForEdit(session.Paths.ToClonePath(p), out _)).ToList();
            if (needLock.Count == 0)
                return true;
            if (NipaLocks.LockBlocking(session, needLock.Select(session.Paths.ToClonePath).ToList(), out var error))
                return true;
            outNotEditablePaths?.AddRange(needLock);
            NipaLocks.Tell("Nipa: Lock", error);
            return false;
        }

        static string[] OnWillSaveAssets(string[] paths)
        {
            var session = NipaHost.Session;
            if (session == null)
                return paths;
            var rejected = new List<string>();
            var allowed = paths.Where(p =>
            {
                if (!NipaHost.IsManaged(p))
                    return true;
                if (session.Locks.CanChange(session.Paths.ToClonePath(p), out var message))
                    return true;
                rejected.Add(message);
                return false;
            }).ToArray();
            if (rejected.Count > 0)
                NipaLocks.Tell("Nipa: not saved", string.Join("\n", rejected));
            return allowed;
        }

        static AssetMoveResult OnWillMoveAsset(string sourcePath, string destinationPath)
        {
            return Guard(sourcePath) && Guard(destinationPath) ? AssetMoveResult.DidNotMove : AssetMoveResult.FailedMove;
        }

        static AssetDeleteResult OnWillDeleteAsset(string assetPath, RemoveAssetOptions options)
        {
            return Guard(assetPath) ? AssetDeleteResult.DidNotDelete : AssetDeleteResult.FailedDelete;
        }

        static bool Guard(string assetPath)
        {
            var session = NipaHost.Session;
            if (session == null || !NipaHost.IsManaged(assetPath))
                return true;
            if (session.Locks.CanChange(session.Paths.ToClonePath(assetPath), out var message))
                return true;
            NipaLocks.Tell("Nipa: locked", message);
            return false;
        }
    }
}
