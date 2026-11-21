using System;
using System.Collections.Generic;
using System.Linq;

namespace Nipa.Editor.Core.Session
{
    public enum SyncGuardStep
    {
        Run,
        ConfirmOverwrite,
        ConfirmUnchecked,
        Stop,
    }

    // Decides what an update, branch switch or tag checkout does after the
    // SyncPlan preview: a failed preview never lets the sync run unasked.
    public static class SyncGuard
    {
        public const int Shown = 10;

        public static SyncGuardStep Decide(IReadOnlyCollection<string> overwritten, Exception error, bool sessionOpen)
        {
            if (!sessionOpen || error is OperationCanceledException || error is ObjectDisposedException)
                return SyncGuardStep.Stop;
            if (error != null || overwritten == null)
                return SyncGuardStep.ConfirmUnchecked;
            return overwritten.Count == 0 ? SyncGuardStep.Run : SyncGuardStep.ConfirmOverwrite;
        }

        public static string OverwriteText(string title, IReadOnlyCollection<string> overwritten)
        {
            var list = string.Join("\n", overwritten.Take(Shown).Select(p => "• " + p)) + (overwritten.Count > Shown ? "\n…" : string.Empty);
            return title + " would replace your local changes to " + overwritten.Count + " file(s):\n" + list
                + "\n\nShelve them first? Apply the shelf afterwards from the Shelves tab.";
        }

        public static string UncheckedText(string operation, Exception error)
        {
            var reason = string.IsNullOrEmpty(error?.Message) ? "the check did not answer" : error.Message;
            return "Could not check your local changes before " + operation + ":\n" + reason
                + "\n\nIf you continue, local changes may be overwritten.";
        }
    }
}
