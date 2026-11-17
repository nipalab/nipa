using System.Linq;
using UnityEditor;

namespace Nipa.Editor
{
    internal static class NipaMenus
    {
        [MenuItem("Window/Nipa/Pending Changes")]
        static void OpenWindow()
        {
            NipaWindow.Open();
        }

        [MenuItem("Assets/Nipa/Stage")]
        static void AssetsStage() => Stage();

        [MenuItem("Assets/Nipa/Stage", true)]
        static bool AssetsStageValid() => HasSelection();

        [MenuItem("Assets/Nipa/Restore")]
        static void AssetsRestore() => Restore();

        [MenuItem("Assets/Nipa/Restore", true)]
        static bool AssetsRestoreValid() => HasSelection();

        [MenuItem("Assets/Nipa/Lock")]
        static void AssetsLock() => Lock();

        [MenuItem("Assets/Nipa/Lock", true)]
        static bool AssetsLockValid() => HasSelection();

        [MenuItem("Assets/Nipa/Unlock")]
        static void AssetsUnlock() => Unlock();

        [MenuItem("Assets/Nipa/Unlock", true)]
        static bool AssetsUnlockValid() => HasSelection();

        [MenuItem("Assets/Nipa/Ask to Release...")]
        static void AssetsRequestRelease() => RequestRelease();

        [MenuItem("Assets/Nipa/Ask to Release...", true)]
        static bool AssetsRequestReleaseValid() => SelectedTheirs() != null;

        [MenuItem("Assets/Nipa/Notify When Free")]
        static void AssetsWatch() => ToggleWatch();

        [MenuItem("Assets/Nipa/Notify When Free", true)]
        static bool AssetsWatchValid() => WatchValid("Assets/Nipa/Notify When Free");

        [MenuItem("Assets/Nipa/Compare with Last Pushed")]
        static void AssetsCompare() => CompareSelected();

        [MenuItem("Assets/Nipa/Compare with Last Pushed", true)]
        static bool AssetsCompareValid() => SelectedComparable() != null;

        [MenuItem("Assets/Nipa/Refresh")]
        static void AssetsRefresh() => NipaHost.Refresh();

        [MenuItem("Assets/Nipa/Refresh", true)]
        static bool AssetsRefreshValid() => NipaHost.Session != null;

        [MenuItem("CONTEXT/Nipa/Stage")]
        static void ContextStage() => Stage();

        [MenuItem("CONTEXT/Nipa/Stage", true)]
        static bool ContextStageValid() => HasSelection();

        [MenuItem("CONTEXT/Nipa/Restore")]
        static void ContextRestore() => Restore();

        [MenuItem("CONTEXT/Nipa/Restore", true)]
        static bool ContextRestoreValid() => HasSelection();

        [MenuItem("CONTEXT/Nipa/Lock")]
        static void ContextLock() => Lock();

        [MenuItem("CONTEXT/Nipa/Lock", true)]
        static bool ContextLockValid() => HasSelection();

        [MenuItem("CONTEXT/Nipa/Unlock")]
        static void ContextUnlock() => Unlock();

        [MenuItem("CONTEXT/Nipa/Unlock", true)]
        static bool ContextUnlockValid() => HasSelection();

        [MenuItem("CONTEXT/Nipa/Ask to Release...")]
        static void ContextRequestRelease() => RequestRelease();

        [MenuItem("CONTEXT/Nipa/Ask to Release...", true)]
        static bool ContextRequestReleaseValid() => SelectedTheirs() != null;

        [MenuItem("CONTEXT/Nipa/Notify When Free")]
        static void ContextWatch() => ToggleWatch();

        [MenuItem("CONTEXT/Nipa/Notify When Free", true)]
        static bool ContextWatchValid() => WatchValid("CONTEXT/Nipa/Notify When Free");

        [MenuItem("CONTEXT/Nipa/Compare with Last Pushed")]
        static void ContextCompare() => CompareSelected();

        [MenuItem("CONTEXT/Nipa/Compare with Last Pushed", true)]
        static bool ContextCompareValid() => SelectedComparable() != null;

        [MenuItem("CONTEXT/Nipa/Refresh")]
        static void ContextRefresh() => NipaHost.Refresh();

        internal static bool HasSelection()
        {
            return NipaHost.Session != null && Selection.assetGUIDs.Length > 0;
        }

        internal static void Stage()
        {
            var paths = NipaHost.SelectedClonePaths();
            if (paths.Count > 0)
                NipaHost.Run("Stage", (s, _) => s.StageAsync(paths));
        }

        internal static string SelectedTheirs()
        {
            var session = NipaHost.Session;
            if (session == null || Selection.assetGUIDs.Length != 1)
                return null;
            var path = NipaHost.SelectedClonePaths().FirstOrDefault();
            return path != null && session.Locks.Lookup(path).State == Core.Locks.LockState.Theirs ? path : null;
        }

        internal static string SelectedComparable()
        {
            var session = NipaHost.Session;
            if (session == null || Selection.assetGUIDs.Length != 1)
                return null;
            var path = NipaHost.SelectedClonePaths().FirstOrDefault();
            return path != null && session.CanCompareWithPushed(path) ? path : null;
        }

        static void CompareSelected()
        {
            var path = SelectedComparable();
            if (path != null)
                CompareWithPushed(path);
        }

        internal static void CompareWithPushed(string clonePath)
        {
            var sides = Core.Session.NipaSession.PushedSides(clonePath);
            NipaImageCompare.Open(sides.Ours.Path, sides.Ours, "Last pushed", sides.Theirs, "Working copy");
        }

        static void RequestRelease()
        {
            var path = SelectedTheirs();
            if (path != null)
                NipaLocks.RequestRelease(path);
        }

        static void ToggleWatch()
        {
            var path = SelectedTheirs();
            if (path != null)
                NipaLocks.ToggleWatch(path);
        }

        static bool WatchValid(string item)
        {
            var path = SelectedTheirs();
            Menu.SetChecked(item, path != null && NipaLocks.IsWatched(path));
            return path != null;
        }

        internal static void Lock() => NipaLocks.Lock(NipaHost.SelectedClonePaths());

        internal static void Unlock() => NipaLocks.Unlock(NipaHost.SelectedClonePaths());

        internal static void Restore()
        {
            var session = NipaHost.Session;
            if (session == null)
                return;
            var paths = session.Restorable(NipaHost.SelectedClonePaths());
            if (paths.Count == 0)
            {
                EditorUtility.DisplayDialog("Nipa: Restore", "Nothing to restore: the selection has no modified, deleted or missing tracked files (staged files must be unstaged first).", "OK");
                return;
            }
            if (!EditorUtility.DisplayDialog("Nipa: Restore", "Discard local changes to " + paths.Count + " selected path(s)?", "Restore", "Cancel"))
                return;
            NipaHost.Run("Restore", async (s, _) =>
            {
                await s.RestoreAsync(paths);
            }, refreshAssets: true);
        }
    }
}
