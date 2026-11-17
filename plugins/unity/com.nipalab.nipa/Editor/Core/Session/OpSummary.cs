using System;
using Nipa.Editor.Core.Daemon;

namespace Nipa.Editor.Core.Session
{
    public static class OpSummary
    {
        public static string Describe(OpResult result)
        {
            if (result?.Merge != null)
            {
                var m = result.Merge;
                if (m.Conflicts != null && m.Conflicts.Count > 0)
                    return m.Conflicts.Count + " conflict(s) to resolve in the Conflicts tab";
                if (m.Aborted)
                    return "merge aborted";
                if (m.UpToDate)
                    return "already up to date";
                if (m.FastForwarded)
                    return "fast-forwarded";
                return m.MergeCommitted ? "merged and committed" : "merged";
            }
            if (result?.Shelve?.Shelf != null)
            {
                var shelf = result.Shelve.Shelf;
                return "shelved " + (shelf.Entries?.Count ?? 0) + " file(s) as \"" + shelf.Name + "\"";
            }
            if (result?.ShelveApply != null)
            {
                var a = result.ShelveApply;
                if (a.Conflicts != null && a.Conflicts.Count > 0)
                    return a.Conflicts.Count + " conflict(s) to resolve in the Conflicts tab";
                if (a.Aborted)
                    return "shelf apply aborted";
                return a.Dropped ? "shelf applied and deleted" : "shelf applied";
            }
            return null;
        }

        public static bool HasConflicts(OpResult result)
        {
            return (result?.Merge?.Conflicts?.Count ?? 0) > 0 || (result?.ShelveApply?.Conflicts?.Count ?? 0) > 0;
        }

        public static string Describe(TeamEvent e)
        {
            switch (e?.Kind)
            {
                case "pushed":
                    return e.Actor + " pushed " + e.Count + " commit(s) to " + e.Branch + ": " + e.Message;
                case "review_requested":
                    return e.Actor + " requested your review on !" + e.Number + " " + e.Message;
                case "lock_released":
                    return e.Path + " is no longer locked by " + e.Actor;
                case "lock_requested":
                    return e.Actor + " asks you to release " + e.Path + (string.IsNullOrWhiteSpace(e.Message) ? string.Empty : ": " + e.Message);
                default:
                    return null;
            }
        }

        public static string ShelfName(string operation, DateTime now)
        {
            return "Before " + operation + " " + now.ToString("yyyy-MM-dd HH:mm");
        }
    }
}
