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
