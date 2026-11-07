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
