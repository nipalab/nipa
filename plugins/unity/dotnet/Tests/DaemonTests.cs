using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;
using Nipa.Editor.Core.Daemon;
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    [TestFixture]
    public class EndpointTests
    {
        [Test]
        public void DefaultPathPerOs()
        {
            string Env(string k) => new Dictionary<string, string> { ["HOME"] = "/h", ["APPDATA"] = "C:/AppData", ["XDG_CONFIG_HOME"] = "/xdg" }.GetValueOrDefault(k);
            Assert.That(Endpoint.DefaultPath(Env, OsKind.Windows).Replace('\\', '/'), Is.EqualTo("C:/AppData/nipa/daemon.json"));
            Assert.That(Endpoint.DefaultPath(Env, OsKind.MacOs).Replace('\\', '/'), Is.EqualTo("/h/Library/Application Support/nipa/daemon.json"));
            Assert.That(Endpoint.DefaultPath(Env, OsKind.Linux).Replace('\\', '/'), Is.EqualTo("/xdg/nipa/daemon.json"));
        }

        [Test]
        public void DefaultPathFallbacks()
        {
            string Env(string k) => k == "USERPROFILE" ? "/u" : null;
            Assert.That(Endpoint.DefaultPath(Env, OsKind.Linux).Replace('\\', '/'), Is.EqualTo("/u/.config/nipa/daemon.json"));
            Assert.That(Endpoint.DefaultPath(Env, OsKind.Windows).Replace('\\', '/'), Is.EqualTo("/u/AppData/Roaming/nipa/daemon.json"));
            Assert.That(Endpoint.DefaultPath(_ => null, OsKind.Linux).Replace('\\', '/'), Does.EndWith(".config/nipa/daemon.json"));
        }

        [Test]
        public void DefaultPathUsesCurrentOs()
        {
            Assert.That(Endpoint.DefaultPath(), Does.EndWith("daemon.json"));
        }

        [Test]
        public void ReadParsesSnakeCase()
        {
            var f = Path.GetTempFileName();
            File.WriteAllText(f, "{\"pid\":7,\"port\":1,\"json_port\":2,\"token\":\"t\",\"version\":\"v1.2.3\"}");
            var ep = Endpoint.Read(f);
            Assert.That((ep.Pid, ep.Port, ep.JsonPort, ep.Token, ep.Version), Is.EqualTo((7, 1, 2, "t", "v1.2.3")));
            File.WriteAllText(f, "null");
            Assert.Throws<InvalidDataException>(() => Endpoint.Read(f));
        }
    }

    [TestFixture]
    public class VersionCompareTests
    {
        [TestCase("v0.9.0", "v1.0.0", true)]
        [TestCase("1.0.0", "v1.0.0", false)]
        [TestCase("v1.2.0-3-gabc", "1.2.0", false)]
        [TestCase("dev-abc", "1.0.0", false)]
        [TestCase("", "1.0.0", false)]
        [TestCase("1.0.0", "", false)]
        [TestCase("1.0.0", "garbage", false)]
        public void Older(string have, string want, bool expected)
        {
            Assert.That(VersionCompare.Older(have, want), Is.EqualTo(expected));
        }
    }

    [TestFixture]
    public class DaemonClientTests
    {
        FakeDaemon fake;

        [SetUp]
        public void SetUp() => fake = new FakeDaemon();

        [TearDown]
        public void TearDown() => fake.Dispose();

        [Test]
        public void RejectsEndpointWithoutJsonPort()
        {
            Assert.Throws<ArgumentException>(() => new DaemonClient(new Endpoint { Port = 1 }));
            Assert.Throws<ArgumentException>(() => new DaemonClient(null));
        }

        [Test]
        public async Task UnarySendsTokenAndSnakeCase()
        {
            fake.On("Status", "{\"staged\":[\"a\"],\"branch\":\"main\",\"pending_remaining\":2,\"head\":null}");
            using var c = new DaemonClient(fake.Endpoint("secret"));
            var res = await c.CallAsync<StatusRequest, StatusResponse>("Status", new StatusRequest { Root = "/r", NoCache = true });
            Assert.That(res.Staged, Is.EqualTo(new[] { "a" }));
            Assert.That(res.PendingRemaining, Is.EqualTo(2));
            Assert.That(fake.Requests.TryDequeue(out var req));
            Assert.That(req.Token, Is.EqualTo("secret"));
            Assert.That(req.Body, Does.Contain("\"no_cache\":true"));
            Assert.That(req.Body, Does.Contain("\"root\":\"/r\""));
        }

        [Test]
        public void MapsErrorBody()
        {
            fake.On("Status", new Reply { Status = 401, Body = "{\"code\":\"Unauthenticated\",\"message\":\"bad token\"}" });
            using var c = new DaemonClient(fake.Endpoint());
            var ex = Assert.ThrowsAsync<DaemonException>(() => c.CallAsync<StatusRequest, StatusResponse>("Status", new StatusRequest()));
            Assert.That((ex.Code, ex.Message), Is.EqualTo((401, "bad token")));
        }

        [Test]
        public void MapsNonJsonAndEmptyErrors()
        {
            fake.On("A", new Reply { Status = 502, Body = "<html>oops</html>" });
            fake.On("B", new Reply { Status = 500, Body = "" });
            fake.On("C", new Reply { Status = 400, Body = "{\"code\":\"x\"}" });
            using var c = new DaemonClient(fake.Endpoint());
            Assert.That(Assert.ThrowsAsync<DaemonException>(() => c.CallAsync<PingRequest, PingResponse>("A", new PingRequest())).Message, Is.EqualTo("<html>oops</html>"));
            Assert.That(Assert.ThrowsAsync<DaemonException>(() => c.CallAsync<PingRequest, PingResponse>("B", new PingRequest())).Message, Is.EqualTo("InternalServerError"));
            Assert.That(Assert.ThrowsAsync<DaemonException>(() => c.CallAsync<PingRequest, PingResponse>("C", new PingRequest())).Code, Is.EqualTo(400));
        }

        [Test]
        public void UnaryTimesOut()
        {
            fake.On("Slow", new Reply { DelayMs = 1500 });
            using var c = new DaemonClient(fake.Endpoint()) { UnaryTimeout = TimeSpan.FromMilliseconds(100) };
            var ex = Assert.ThrowsAsync<DaemonException>(() => c.CallAsync<PingRequest, PingResponse>("Slow", new PingRequest()));
            Assert.That(ex.Code, Is.EqualTo(504));
        }

        [Test]
        public void UnaryCallerCancelPropagates()
        {
            fake.On("Slow", new Reply { DelayMs = 1500 });
            using var c = new DaemonClient(fake.Endpoint());
            using var cts = new CancellationTokenSource(100);
            Assert.CatchAsync<OperationCanceledException>(() => c.CallAsync<PingRequest, PingResponse>("Slow", new PingRequest(), cts.Token));
        }

        [Test]
        public async Task StreamReadsNdjson()
        {
            fake.On("Update", new Reply { Lines = new[] { "{\"queued\":{\"ahead\":1}}", "", "{\"result\":{\"sync\":{\"branch\":\"main\"}}}" } });
            using var c = new DaemonClient(fake.Endpoint());
            var got = new List<OpEvent>();
            await foreach (var ev in c.StreamAsync<UpdateRequest, OpEvent>("Update", new UpdateRequest { Root = "/r" }))
                got.Add(ev);
            Assert.That(got.Count, Is.EqualTo(2));
            Assert.That(got[0].Queued.Ahead, Is.EqualTo(1));
            Assert.That(got[1].Result.Sync.Branch, Is.EqualTo("main"));
        }

        [Test]
        public void StreamErrorStatus()
        {
            fake.On("Update", new Reply { Status = 403, Body = "{\"code\":\"PermissionDenied\",\"message\":\"nope\"}", Lines = null });
            using var c = new DaemonClient(fake.Endpoint());
            var ex = Assert.ThrowsAsync<DaemonException>(async () =>
            {
                await foreach (var _ in c.StreamAsync<UpdateRequest, OpEvent>("Update", new UpdateRequest())) { }
            });
            Assert.That(ex.Code, Is.EqualTo(403));
        }

        [Test]
        public async Task StreamCancelStopsIteration()
        {
            fake.On("WatchChanges", new Reply { Lines = new[] { "{\"paths\":[\"a\"]}" }, Hold = true });
            using var c = new DaemonClient(fake.Endpoint());
            using var cts = new CancellationTokenSource();
            var seen = 0;
            var task = Task.Run(async () =>
            {
                await foreach (var _ in c.StreamAsync<WatchChangesRequest, RepoChange>("WatchChanges", new WatchChangesRequest(), cts.Token))
                {
                    seen++;
                    cts.Cancel();
                }
            });
            Assert.CatchAsync<OperationCanceledException>(async () => await task);
            Assert.That(seen, Is.EqualTo(1));
            await Task.CompletedTask;
        }

        [Test]
        public async Task OpsReturnsResultAndReportsEvents()
        {
            fake.On("Push", new Reply { Lines = new[] { "{\"started\":{\"phase\":\"scan\"}}", "{\"progress\":{\"phase\":\"upload\",\"bytes_done\":5}}", "{\"result\":{\"push\":{\"commit_id\":\"abc\"}}}" } });
            using var c = new DaemonClient(fake.Endpoint());
            var events = new List<OpEvent>();
            var res = await Ops.RunAsync(c, "Push", new PushRequest { Root = "/r", Message = "m" }, events.Add, CancellationToken.None);
            Assert.That(res.Push.CommitId, Is.EqualTo("abc"));
            Assert.That(events.Select(e => e.Started != null || e.Progress != null || e.Result != null), Is.All.True);
            Assert.That(events[1].Progress.BytesDone, Is.EqualTo(5));
        }

        [Test]
        public void OpsFailureAndMissingResult()
        {
            fake.On("Push", new Reply { Lines = new[] { "{\"failure\":{\"code\":409,\"message\":\"locked by bob\"}}" } });
            fake.On("Update", new Reply { Lines = new[] { "{\"queued\":{}}" } });
            using var c = new DaemonClient(fake.Endpoint());
            var ex = Assert.ThrowsAsync<DaemonException>(() => Ops.RunAsync<PushRequest>(c, "Push", new PushRequest(), null, CancellationToken.None));
            Assert.That((ex.Code, ex.Message), Is.EqualTo((409, "locked by bob")));
            var ex2 = Assert.ThrowsAsync<DaemonException>(() => Ops.RunAsync<UpdateRequest>(c, "Update", new UpdateRequest(), null, CancellationToken.None));
            Assert.That(ex2.Code, Is.EqualTo(500));
        }
    }

    [TestFixture]
    public class ConnectorTests
    {
        string dir;
        string path;

        [SetUp]
        public void SetUp()
        {
            dir = Path.Combine(Path.GetTempPath(), "nipa-conn-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(dir);
            path = Path.Combine(dir, "daemon.json");
        }

        [TearDown]
        public void TearDown() => Directory.Delete(dir, true);

        void Publish(FakeDaemon d, string version, int pid = 0)
        {
            var ep = d.Endpoint();
            ep.Pid = pid == 0 ? ep.Pid : pid;
            ep.Version = version;
            File.WriteAllText(path, JsonConvert.SerializeObject(ep, DaemonClient.JsonSettings));
        }

        [Test]
        public async Task AttachesToLiveDaemon()
        {
            using var d = new FakeDaemon();
            d.On("Ping", "{\"version\":\"v1.0.0\",\"pid\":1}");
            Publish(d, "v1.0.0");
            var conn = await Connector.ConnectAsync(new ConnectorOptions { EndpointPath = path, Alive = _ => true, Start = (_, __) => throw new Exception("must not start") }, CancellationToken.None);
            Assert.That(conn.Started, Is.False);
            Assert.That(conn.Version, Is.EqualTo("v1.0.0"));
            conn.Client.Dispose();
        }

        [Test]
        public async Task StartsDaemonWhenNoEndpoint()
        {
            using var d = new FakeDaemon();
            d.On("Ping", "{\"version\":\"v1.0.0\",\"pid\":1}");
            string startedWith = null;
            var opts = new ConnectorOptions
            {
                EndpointPath = path,
                Binary = "/bin/nipa",
                Alive = _ => true,
                PollInterval = TimeSpan.FromMilliseconds(10),
                Start = (bin, ep) =>
                {
                    startedWith = bin + "|" + ep;
                    Publish(d, "v1.0.0");
                    return true;
                },
            };
            var conn = await Connector.ConnectAsync(opts, CancellationToken.None);
            Assert.That(conn.Started, Is.True);
            Assert.That(startedWith, Is.EqualTo("/bin/nipa|" + path));
            conn.Client.Dispose();
        }

        [Test]
        public async Task StaleEndpointIsReplaced()
        {
            using var d = new FakeDaemon();
            d.On("Ping", "{\"version\":\"v1.0.0\",\"pid\":1}");
            Publish(d, "v1.0.0", pid: 424242);
            var alive = false;
            var opts = new ConnectorOptions
            {
                EndpointPath = path,
                Binary = "nipa",
                Alive = _ => alive,
                PollInterval = TimeSpan.FromMilliseconds(10),
                Start = (_, __) => { alive = true; return true; },
            };
            var conn = await Connector.ConnectAsync(opts, CancellationToken.None);
            Assert.That(conn.Started, Is.True);
            conn.Client.Dispose();
        }

        [Test]
        public async Task OutdatedDaemonIsRetired()
        {
            using var d = new FakeDaemon();
            var shut = false;
            d.Handlers["Ping"] = _ => shut ? new Reply { Status = 503, Body = "{}" } : new Reply { Body = "{\"version\":\"v0.1.0\"}" };
            d.Handlers["Shutdown"] = _ => { shut = true; return new Reply(); };
            Publish(d, "v0.1.0");
            using var fresh = new FakeDaemon();
            fresh.On("Ping", "{\"version\":\"v2.0.0\"}");
            var opts = new ConnectorOptions
            {
                EndpointPath = path,
                Binary = "nipa",
                MinVersion = "v1.0.0",
                Alive = _ => true,
                PollInterval = TimeSpan.FromMilliseconds(10),
                Start = (_, __) => { Publish(fresh, "v2.0.0"); return true; },
            };
            var conn = await Connector.ConnectAsync(opts, CancellationToken.None);
            Assert.That(shut, Is.True);
            Assert.That(conn.Version, Is.EqualTo("v2.0.0"));
            Assert.That(conn.Started, Is.True);
            conn.Client.Dispose();
        }

        [Test]
        public void TimesOutWhenDaemonNeverAppears()
        {
            var opts = new ConnectorOptions
            {
                EndpointPath = path,
                Binary = "nipa",
                StartTimeout = TimeSpan.FromMilliseconds(150),
                PollInterval = TimeSpan.FromMilliseconds(20),
                Start = (_, __) => true,
            };
            Assert.ThrowsAsync<TimeoutException>(() => Connector.ConnectAsync(opts, CancellationToken.None));
        }

        [Test]
        public void FailsWhenStartFails()
        {
            var opts = new ConnectorOptions { EndpointPath = path, Binary = "nipa", Start = (_, __) => false };
            Assert.ThrowsAsync<InvalidOperationException>(() => Connector.ConnectAsync(opts, CancellationToken.None));
        }

        [Test]
        public void CorruptEndpointIsTreatedAsMissing()
        {
            File.WriteAllText(path, "not json");
            var opts = new ConnectorOptions { EndpointPath = path, Binary = "nipa", Start = (_, __) => false };
            Assert.ThrowsAsync<InvalidOperationException>(() => Connector.ConnectAsync(opts, CancellationToken.None));
        }

        [Test]
        public void ResolveBinaryOrder()
        {
            string None(string _) => null;
            Assert.That(Connector.ResolveBinary(new ConnectorOptions { Binary = "/set" }, None), Is.EqualTo("/set"));
            Assert.That(Connector.ResolveBinary(new ConnectorOptions(), k => k == "NIPA_BIN" ? "/env" : null), Is.EqualTo("/env"));
            Assert.That(Connector.ResolveBinary(new ConnectorOptions { Which = n => "/path/" + n }, None), Does.StartWith("/path/nipa"));
            Assert.Throws<FileNotFoundException>(() => Connector.ResolveBinary(new ConnectorOptions { Which = _ => null }, None));
        }

        [Test]
        public void ResolveBinaryScansPath()
        {
            var bin = Path.Combine(dir, Path.DirectorySeparatorChar == '\\' ? "nipa.exe" : "nipa");
            File.WriteAllText(bin, "");
            var sep = Path.PathSeparator.ToString();
            string Env(string k) => k == "PATH" ? sep + Path.Combine(dir, "missing") + sep + dir : null;
            Assert.That(Connector.ResolveBinary(new ConnectorOptions(), Env), Is.EqualTo(bin));
            Assert.Throws<FileNotFoundException>(() => Connector.ResolveBinary(new ConnectorOptions(), _ => null));
        }

        [Test]
        public async Task DefaultsUseRealProcessProbe()
        {
            using var d = new FakeDaemon();
            d.On("Ping", "{\"version\":\"v1.0.0\"}");
            Publish(d, "v1.0.0");
            var conn = await Connector.ConnectAsync(new ConnectorOptions { EndpointPath = path, Env = _ => null }, CancellationToken.None);
            Assert.That(conn.Started, Is.False);
            conn.Client.Dispose();
            Publish(d, "v1.0.0", pid: 2147483000);
            var opts = new ConnectorOptions { EndpointPath = path, Binary = "nipa", Start = (_, __) => false };
            Assert.ThrowsAsync<InvalidOperationException>(() => Connector.ConnectAsync(opts, CancellationToken.None));
        }

        [Test]
        public void DefaultStartRunsBinary()
        {
            if (!OperatingSystem.IsLinux() && !OperatingSystem.IsMacOS())
                Assert.Ignore("needs a POSIX shell");
            RunDefaultStart();
        }

        void RunDefaultStart()
        {
            var script = Path.Combine(dir, "fake-nipa.sh");
            var marker = Path.Combine(dir, "marker");
            File.WriteAllText(script, "#!/bin/sh\necho \"$@\" > \"" + marker + "\"\n");
            File.SetUnixFileMode(script, UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute);
            var opts = new ConnectorOptions { EndpointPath = path, Binary = script, StartTimeout = TimeSpan.FromMilliseconds(100), PollInterval = TimeSpan.FromMilliseconds(10) };
            Assert.ThrowsAsync<TimeoutException>(() => Connector.ConnectAsync(opts, CancellationToken.None));
            SpinWait.SpinUntil(() => File.Exists(marker), 3000);
            Assert.That(File.ReadAllText(marker), Does.Contain("serve --endpoint"));
        }
    }
}
