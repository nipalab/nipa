using System.Collections.Generic;
using System.Linq;
using Nipa.Editor.Core.Daemon;

namespace Nipa.Editor.Core.Session
{
    public static class PushChecks
    {
        public const int MaxUnused = 10;

        public static List<string> Warnings(PushPlanResponse plan)
        {
            var result = new List<string>();
            if (plan == null)
                return result;
            foreach (var v in plan.AssetViolations ?? new List<AssetViolation>())
                result.Add(v.Path + ": " + Violation(v));
            var impact = plan.AssetImpact;
            foreach (var b in impact?.Broken ?? new List<BrokenReference>())
                result.Add(b.From + ": broken reference to " + (string.IsNullOrEmpty(b.Target) ? b.Raw : b.Target)
                    + (string.IsNullOrEmpty(b.Reason) ? string.Empty : " (" + b.Reason.Replace('_', ' ') + ")"));
            var unused = impact?.NewlyUnused ?? new List<string>();
            foreach (var u in unused.Take(MaxUnused))
                result.Add(u + ": no longer used by anything");
            if (unused.Count > MaxUnused)
                result.Add((unused.Count - MaxUnused) + " more file(s) no longer used by anything");
            if (impact != null && impact.Truncated)
                result.Add("The reference scan stopped early; some references were not checked.");
            result.AddRange((plan.Warnings ?? new List<string>()).Where(w => !string.IsNullOrWhiteSpace(w)));
            return result;
        }

        static string Violation(AssetViolation v)
        {
            if (!string.IsNullOrWhiteSpace(v.Message))
                return v.Message;
            var text = v.Limit ?? "budget";
            if (!string.IsNullOrEmpty(v.Actual))
                text += " is " + v.Actual;
            if (!string.IsNullOrEmpty(v.Max))
                text += ", max " + v.Max;
            return text;
        }
    }
}
