using System;
using System.Collections.Generic;
using System.Linq;
using Nipa.Editor.Core.Session;
using UnityEditor;

namespace Nipa.Editor
{
    internal static class NipaLocks
    {
        public static void Lock(List<string> clonePaths, Action afterLocked = null)
        {
            if (clonePaths.Count == 0)
                return;
            NipaHost.Run("Lock", async (s, _) =>
            {
                var result = await s.LockAsync(clonePaths);
                if (result.Failed.Count > 0)
                    throw new InvalidOperationException(Describe(result));
            }, onSuccess: afterLocked);
        }

        public static void Unlock(List<string> clonePaths)
        {
            if (clonePaths.Count > 0)
                NipaHost.Run("Unlock", (s, _) => s.UnlockAsync(clonePaths));
        }

        public static void RequestRelease(string clonePath)
        {
            var session = NipaHost.Session;
            if (session == null)
                return;
            var info = session.Locks.Lookup(clonePath);
            if (info.State != Core.Locks.LockState.Theirs)
            {
                EditorUtility.DisplayDialog("Nipa: Ask to release", clonePath + " is not locked by someone else.", "OK");
                return;
            }
            NipaPrompt.Ask("Nipa: Ask to release", "Ask " + info.Holder + " to release " + info.LockPath + ". Add an optional note:",
                string.Empty, "Send request", true, note =>
                    NipaHost.Run("Ask to release", (s, _) => s.RequestReleaseAsync(clonePath, note), onSuccess: () =>
                        EditorUtility.DisplayDialog("Nipa: Ask to release", "Request sent to " + info.Holder + ".", "OK")));
        }

        public static bool IsWatched(string clonePath) => NipaHost.Watches.IsWatched(clonePath);

        public static void ToggleWatch(string clonePath)
        {
            if (IsWatched(clonePath))
                NipaHost.Watches.Unwatch(clonePath);
            else
                NipaHost.Watches.Watch(clonePath);
        }

        public static string Describe(LockResult result)
        {
            var lines = result.Failed.Select(f => "• " + f.Path + " — " + (f.Holder != null ? "locked by " + f.Holder : f.Message));
            return "Could not lock:\n" + string.Join("\n", lines);
        }

        public static bool LockBlocking(NipaSession session, List<string> clonePaths, out string error)
        {
            error = null;
            LockResult result;
            try
            {
                var task = System.Threading.Tasks.Task.Run(() => session.LockAsync(clonePaths));
                if (!task.Wait(TimeSpan.FromSeconds(20)))
                {
                    error = "Timed out waiting for the Nipa daemon.";
                    return false;
                }
                result = task.Result;
            }
            catch (AggregateException e)
            {
                error = e.GetBaseException().Message;
                return false;
            }
            if (result.Failed.Count == 0)
                return true;
            error = Describe(result);
            return false;
        }

        public static void Tell(string title, string message)
        {
            if (UnityEngine.Application.isBatchMode)
            {
                UnityEngine.Debug.LogWarning("[Nipa] " + title + ": " + message);
                return;
            }
            EditorApplication.delayCall += () => EditorUtility.DisplayDialog(title, message, "OK");
        }
    }
}
