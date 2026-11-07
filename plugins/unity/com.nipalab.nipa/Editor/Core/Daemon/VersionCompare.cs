using System;

namespace Nipa.Editor.Core.Daemon
{
    public static class VersionCompare
    {
        public static bool Older(string have, string want)
        {
            if (!TryParse(have, out var a) || !TryParse(want, out var b))
                return false;
            return a.CompareTo(b) < 0;
        }

        static bool TryParse(string raw, out Version v)
        {
            v = null;
            if (string.IsNullOrEmpty(raw))
                return false;
            var s = raw.TrimStart('v', 'V');
            var cut = s.IndexOfAny(new[] { '-', '+' });
            if (cut >= 0)
                s = s.Substring(0, cut);
            if (s.IndexOf('.') < 0)
                return false;
            return Version.TryParse(s, out v);
        }
    }
}
