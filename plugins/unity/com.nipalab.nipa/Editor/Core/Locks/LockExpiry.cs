using System;
using Nipa.Editor.Core.Daemon;

namespace Nipa.Editor.Core.Locks
{
    public static class LockExpiry
    {
        public static readonly TimeSpan MinTtl = TimeSpan.FromMinutes(1);
        public static readonly TimeSpan MaxTtl = TimeSpan.FromDays(7);

        public static string Describe(FileLockDetail l, DateTimeOffset now)
        {
            if (l?.ExpiresAt == null)
                return null;
            var left = l.ExpiresAt.Value - now;
            return left <= TimeSpan.Zero ? "expired" : "expires in " + Span(left);
        }

        public static string Span(TimeSpan t)
        {
            if (t.TotalDays >= 1)
                return (int)t.TotalDays + "d" + (t.Hours > 0 ? " " + t.Hours + "h" : string.Empty);
            if (t.TotalHours >= 1)
                return (int)t.TotalHours + "h" + (t.Minutes > 0 ? " " + t.Minutes + "m" : string.Empty);
            return Math.Max(1, (int)Math.Ceiling(t.TotalMinutes)) + "m";
        }

        public static TimeSpan? Ttl(int hours)
        {
            return hours <= 0 ? (TimeSpan?)null : Clamp(TimeSpan.FromHours(hours));
        }

        public static TimeSpan RenewTtl(TimeSpan? configured)
        {
            return Clamp(configured ?? TimeSpan.FromHours(1));
        }

        static TimeSpan Clamp(TimeSpan t)
        {
            if (t < MinTtl)
                return MinTtl;
            return t > MaxTtl ? MaxTtl : t;
        }
    }
}
