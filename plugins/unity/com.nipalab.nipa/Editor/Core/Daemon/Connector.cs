using System;
using System.Diagnostics;
using System.IO;
using System.Threading;
using System.Threading.Tasks;

namespace Nipa.Editor.Core.Daemon
{
    public sealed class ConnectorOptions
    {
        public string EndpointPath { get; set; }
        public string Binary { get; set; }
        public string MinVersion { get; set; }
        public TimeSpan StartTimeout { get; set; } = TimeSpan.FromSeconds(10);
        public TimeSpan PollInterval { get; set; } = TimeSpan.FromMilliseconds(100);
        public Func<string, string> Env { get; set; }
        public Func<string, string, bool> Start { get; set; }
        public Func<int, bool> Alive { get; set; }
        public Func<string, string> Which { get; set; }
    }

    public sealed class Connection
    {
        public DaemonClient Client { get; set; }
        public string Version { get; set; }
        public bool Started { get; set; }
    }

    public static class Connector
    {
        public const string EnvNipaBinary = "NIPA_BIN";

        public static async Task<Connection> ConnectAsync(ConnectorOptions opts, CancellationToken ct)
        {
            var env = opts.Env ?? Environment.GetEnvironmentVariable;
            var path = string.IsNullOrEmpty(opts.EndpointPath) ? Endpoint.DefaultPath(env) : opts.EndpointPath;
            var alive = opts.Alive ?? ProcessAlive;

            var existing = await TryAttachAsync(path, alive, ct).ConfigureAwait(false);
            if (existing != null)
            {
                if (!VersionCompare.Older(existing.Version, opts.MinVersion))
                    return existing;
                await TryShutdownAsync(existing, opts, ct).ConfigureAwait(false);
            }

            var bin = ResolveBinary(opts, env);
            var start = opts.Start ?? StartDetached;
            if (!start(bin, string.IsNullOrEmpty(opts.EndpointPath) ? null : opts.EndpointPath))
                throw new InvalidOperationException("could not start " + bin + " serve");

            var deadline = DateTime.UtcNow + opts.StartTimeout;
            while (true)
            {
                ct.ThrowIfCancellationRequested();
                var conn = await TryAttachAsync(path, alive, ct).ConfigureAwait(false);
                if (conn != null && !VersionCompare.Older(conn.Version, opts.MinVersion))
                {
                    conn.Started = true;
                    return conn;
                }
                conn?.Client.Dispose();
                if (DateTime.UtcNow >= deadline)
                    throw new TimeoutException("nipa daemon did not start within " + opts.StartTimeout.TotalSeconds + "s");
                await Task.Delay(opts.PollInterval, ct).ConfigureAwait(false);
            }
        }

        static async Task<Connection> TryAttachAsync(string path, Func<int, bool> alive, CancellationToken ct)
        {
            Endpoint ep;
            try
            {
                ep = Endpoint.Read(path);
            }
            catch (Exception e) when (e is IOException || e is UnauthorizedAccessException || e is Newtonsoft.Json.JsonException || e is InvalidDataException)
            {
                return null;
            }
            if (ep.JsonPort <= 0 || !alive(ep.Pid))
                return null;
            var client = new DaemonClient(ep) { UnaryTimeout = TimeSpan.FromSeconds(2) };
            try
            {
                var pong = await client.CallAsync<PingRequest, PingResponse>("Ping", new PingRequest(), ct).ConfigureAwait(false);
                client.UnaryTimeout = TimeSpan.FromSeconds(30);
                return new Connection { Client = client, Version = pong.Version };
            }
            catch (Exception e) when (e is DaemonException || e is System.Net.Http.HttpRequestException)
            {
                client.Dispose();
                return null;
            }
        }

        static async Task TryShutdownAsync(Connection conn, ConnectorOptions opts, CancellationToken ct)
        {
            try
            {
                await conn.Client.CallAsync<ShutdownRequest, ShutdownResponse>("Shutdown", new ShutdownRequest(), ct).ConfigureAwait(false);
            }
            catch (Exception e) when (e is DaemonException || e is System.Net.Http.HttpRequestException)
            {
            }
            var deadline = DateTime.UtcNow + opts.StartTimeout;
            while (DateTime.UtcNow < deadline)
            {
                try
                {
                    await conn.Client.CallAsync<PingRequest, PingResponse>("Ping", new PingRequest(), ct).ConfigureAwait(false);
                }
                catch (Exception e) when (e is DaemonException || e is System.Net.Http.HttpRequestException)
                {
                    break;
                }
                await Task.Delay(opts.PollInterval, ct).ConfigureAwait(false);
            }
            conn.Client.Dispose();
        }

        public static string ResolveBinary(ConnectorOptions opts, Func<string, string> env)
        {
            if (!string.IsNullOrEmpty(opts.Binary))
                return opts.Binary;
            var fromEnv = env(EnvNipaBinary);
            if (!string.IsNullOrEmpty(fromEnv))
                return fromEnv;
            var name = Path.DirectorySeparatorChar == '\\' ? "nipa.exe" : "nipa";
            var found = (opts.Which ?? (n => WhichOnPath(n, env)))(name);
            if (found == null)
                throw new FileNotFoundException("nipa CLI not found: set the binary path in the Nipa settings, NIPA_BIN or add it to PATH");
            return found;
        }

        static string WhichOnPath(string name, Func<string, string> env)
        {
            var path = env("PATH");
            if (string.IsNullOrEmpty(path))
                return null;
            foreach (var dir in path.Split(Path.PathSeparator))
            {
                if (dir.Length == 0)
                    continue;
                var candidate = Path.Combine(dir, name);
                if (File.Exists(candidate))
                    return candidate;
            }
            return null;
        }

        static bool StartDetached(string bin, string endpoint)
        {
            var info = new ProcessStartInfo(bin)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
            };
            info.Arguments = endpoint == null ? "serve" : "serve --endpoint \"" + endpoint + "\"";
            using (var p = Process.Start(info))
                return p != null;
        }

        static bool ProcessAlive(int pid)
        {
            try
            {
                using (var p = Process.GetProcessById(pid))
                    return !p.HasExited;
            }
            catch (ArgumentException)
            {
                return false;
            }
            catch (InvalidOperationException)
            {
                return false;
            }
        }
    }

    public sealed class ShutdownRequest { }

    public sealed class ShutdownResponse { }
}
