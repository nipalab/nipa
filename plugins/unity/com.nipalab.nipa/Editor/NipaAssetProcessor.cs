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

        static bool MakeEditable(string[] paths)
        {
            var session = NipaHost.Session;
            if (session == null)
                return true;
            var clone = paths.Where(NipaHost.IsManaged).Select(session.Paths.ToClonePath).ToList();
            if (clone.Count == 0)
                return true;
            if (clone.Any(p => !session.Locks.CanChange(p, out _)))
            {
                NipaLocks.Tell("Nipa: Lock", string.Join("\n", Blocked(clone)));
                return false;
            }
            var locked = clone.Where(p => !session.IsOpenForEdit(p, out _)).ToList();
            if (locked.Count == 0)
                return true;
            if (NipaLocks.LockBlocking(session, locked, out var error))
                return true;
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
            return Guard(sourcePath) ? AssetMoveResult.DidNotMove : AssetMoveResult.FailedMove;
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

        static IEnumerable<string> Blocked(List<string> clone)
        {
            var session = NipaHost.Session;
            foreach (var p in clone)
            {
                if (!session.Locks.CanChange(p, out var message))
                    yield return message;
            }
        }
    }
}
