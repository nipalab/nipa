using System;
using System.IO;
using Newtonsoft.Json;

namespace Nipa.Editor.Core.Daemon
{
    public sealed class Endpoint
    {
        public int Pid { get; set; }
        public int Port { get; set; }
        public int JsonPort { get; set; }
        public string Token { get; set; }
        public string Version { get; set; }

        public static string DefaultPath(Func<string, string> env = null, OsKind? os = null)
        {
            env = env ?? Environment.GetEnvironmentVariable;
            var kind = os ?? CurrentOs();
            string dir;
            switch (kind)
            {
                case OsKind.Windows:
                    dir = env("APPDATA");
                    if (string.IsNullOrEmpty(dir))
                        dir = Path.Combine(Home(env), "AppData", "Roaming");
                    break;
                case OsKind.MacOs:
                    dir = Path.Combine(Home(env), "Library", "Application Support");
                    break;
                default:
                    dir = env("XDG_CONFIG_HOME");
                    if (string.IsNullOrEmpty(dir))
                        dir = Path.Combine(Home(env), ".config");
                    break;
            }
            return Path.Combine(dir, "nipa", "daemon.json");
        }

        public static Endpoint Read(string path)
        {
            var ep = JsonConvert.DeserializeObject<Endpoint>(File.ReadAllText(path), DaemonClient.JsonSettings);
            if (ep == null)
                throw new InvalidDataException("empty endpoint file " + path);
            return ep;
        }

        static string Home(Func<string, string> env)
        {
            var home = env("HOME");
            if (string.IsNullOrEmpty(home))
                home = env("USERPROFILE");
            return home ?? string.Empty;
        }

        static OsKind CurrentOs()
        {
            switch (Environment.OSVersion.Platform)
            {
                case PlatformID.Win32NT:
                    return OsKind.Windows;
                case PlatformID.MacOSX:
                    return OsKind.MacOs;
                default:
                    return Directory.Exists("/Applications") && Directory.Exists("/Users")
                        ? OsKind.MacOs
                        : OsKind.Linux;
            }
        }
    }

    public enum OsKind
    {
        Windows,
        MacOs,
        Linux,
    }
}
