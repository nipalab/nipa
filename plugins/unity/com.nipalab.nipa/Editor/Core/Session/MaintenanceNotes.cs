using System;
using System.Collections.Generic;
using System.Linq;
using Nipa.Editor.Core.Daemon;

namespace Nipa.Editor.Core.Session
{
    public sealed class MaintenanceNotice
    {
        public string Scope { get; }
        public MaintenanceWindow Window { get; }
        public string Text { get; }

        public MaintenanceNotice(string scope, MaintenanceWindow window, string text)
        {
            Scope = scope;
            Window = window;
            Text = text;
        }

        public bool Active => Window.Active;
        public bool BlocksWrites => Window.Active;
        public bool BlocksSync => Window.Active && (Window.Mode == "full" || Window.Mode == "frozen");
    }

    public static class MaintenanceNotes
    {
        public static Func<DateTimeOffset, string> Format { get; set; } = t => t.ToLocalTime().ToString("yyyy-MM-dd HH:mm");

        public static string ModeText(string mode)
        {
            switch (mode)
            {
                case "read_only":
                    return "read-only";
                case "full":
                    return "closed";
                case "frozen":
                    return "frozen";
                default:
                    return mode ?? string.Empty;
            }
        }

        static int Rank(string mode)
        {
            switch (mode)
            {
                case "read_only":
                    return 1;
                case "full":
                    return 2;
                case "frozen":
                    return 3;
                default:
                    return 0;
            }
        }

        static string ScopeText(string scope)
        {
            switch (scope)
            {
                case "org":
                    return "Organization";
                case "project":
                    return "Project";
                default:
                    return "Server";
            }
        }

        public static string ScopeOf(MaintenanceWindow w)
        {
            if (!string.IsNullOrEmpty(w?.Scope))
                return w.Scope;
            if (!string.IsNullOrEmpty(w?.Project))
                return "project";
            return string.IsNullOrEmpty(w?.Org) ? "server" : "org";
        }

        public static MaintenanceNotice Current(MaintenanceState state, DateTimeOffset now)
        {
            if (state == null)
                return null;
            var windows = new List<KeyValuePair<string, MaintenanceWindow>>
            {
                new KeyValuePair<string, MaintenanceWindow>("server", state.Server),
                new KeyValuePair<string, MaintenanceWindow>("org", state.Org),
                new KeyValuePair<string, MaintenanceWindow>("project", state.Project),
            }.Where(kv => kv.Value != null && !string.IsNullOrEmpty(kv.Value.Mode)).ToList();
            var active = windows.Where(kv => kv.Value.Active).ToList();
            if (active.Count > 0)
            {
                var top = active.Aggregate((a, b) => Rank(b.Value.Mode) > Rank(a.Value.Mode) ? b : a);
                return new MaintenanceNotice(top.Key, top.Value, Describe(top.Key, top.Value));
            }
            var next = windows.Where(kv => kv.Value.StartsAt.HasValue && kv.Value.StartsAt.Value > now)
                .OrderBy(kv => kv.Value.StartsAt.Value).FirstOrDefault();
            return next.Value == null ? null : new MaintenanceNotice(next.Key, next.Value, Describe(next.Key, next.Value));
        }

        public static string Describe(string scope, MaintenanceWindow w)
        {
            var parts = new List<string>();
            var head = ScopeText(scope) + " maintenance (" + ModeText(w.Mode) + ")";
            if (!w.Active && w.StartsAt.HasValue)
                head += " starts " + Format(w.StartsAt.Value);
            parts.Add(head + ".");
            if (!string.IsNullOrWhiteSpace(w.Message))
                parts.Add(w.Message.Trim());
            if (w.EndsAt.HasValue)
                parts.Add("Estimated end " + Format(w.EndsAt.Value) + ".");
            parts.Add(Effect(w.Mode));
            return string.Join(" ", parts);
        }

        static string Effect(string mode)
        {
            switch (mode)
            {
                case "read_only":
                    return "Push, locks and merges are refused; Update still works.";
                case "frozen":
                    return "Writes and sign-in are refused.";
                default:
                    return "The server refuses every request.";
            }
        }

        public static string Describe(TeamEvent e)
        {
            var w = e?.Maintenance;
            if (w == null)
                return null;
            var what = ScopeText(ScopeOf(w)) + " maintenance (" + ModeText(w.Mode) + ")";
            switch (e.Message)
            {
                case "scheduled":
                    return what + " scheduled" + (w.StartsAt.HasValue ? " for " + Format(w.StartsAt.Value) : string.Empty) + Note(w);
                case "started":
                    return what + " started" + Note(w);
                case "updated":
                    return what + " updated" + Note(w);
                case "ended":
                    return what + " ended";
                default:
                    return what + Note(w);
            }
        }

        static string Note(MaintenanceWindow w)
        {
            var text = w.EndsAt.HasValue ? ", estimated end " + Format(w.EndsAt.Value) : string.Empty;
            return string.IsNullOrWhiteSpace(w.Message) ? text : text + ": " + w.Message.Trim();
        }

        public static string ErrorText(Exception e)
        {
            if (e is AggregateException agg)
                e = agg.GetBaseException();
            if (e is DaemonException d && d.Maintenance != null)
            {
                var w = d.Maintenance;
                var text = d.Message;
                if (w.EndsAt.HasValue)
                    text += "\nEstimated end " + Format(w.EndsAt.Value) + ".";
                return text;
            }
            return e?.Message;
        }
    }
}
