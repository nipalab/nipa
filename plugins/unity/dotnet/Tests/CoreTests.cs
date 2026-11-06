using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Session;
using Nipa.Editor.Core.Status;
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    [TestFixture]
    public class ProjectPathsTests
    {
        static string Sep(string p) => p.Replace('/', Path.DirectorySeparatorChar);

        [Test]
        public void SubfolderProjectMapsBothWays()
        {
            var root = Path.GetFullPath(Sep("/clone"));
            var p = new ProjectPaths(Path.Combine(root, "game") + Path.DirectorySeparatorChar, root);
            Assert.That(p.Prefix, Is.EqualTo("game"));
            Assert.That(p.ToClonePath("Assets/a.prefab"), Is.EqualTo("game/Assets/a.prefab"));
            Assert.That(p.ToClonePath("Assets\\b.png"), Is.EqualTo("game/Assets/b.png"));
            Assert.That(p.ToClonePath(""), Is.EqualTo("game"));
            Assert.That(p.ToUnityPath("game/Assets/a.prefab"), Is.EqualTo("Assets/a.prefab"));
            Assert.That(p.ToUnityPath("game"), Is.EqualTo(""));
            Assert.That(p.ToUnityPath("other/x"), Is.Null);
            Assert.That(p.ToUnityPath("gamex/x"), Is.Null);
            Assert.That(p.IsInProject("game/Assets"), Is.True);
            Assert.That(p.IsInProject("docs/readme.md"), Is.False);
        }

        [Test]
        public void ProjectAtCloneRoot()
        {
            var root = Path.GetFullPath(Sep("/clone"));
            var p = new ProjectPaths(root, root);
            Assert.That(p.Prefix, Is.EqualTo(""));
            Assert.That(p.ToClonePath("/Assets/a"), Is.EqualTo("Assets/a"));
            Assert.That(p.ToUnityPath("/Assets/a"), Is.EqualTo("Assets/a"));
        }

        [Test]
        public void ProjectOutsideCloneThrows()
        {
            Assert.Throws<ArgumentException>(() => new ProjectPaths(Path.GetFullPath(Sep("/a/b")), Path.GetFullPath(Sep("/a/c"))));
        }

        [Test]
        public void DiscoverWalksUp()
        {
            var tmp = Path.Combine(Path.GetTempPath(), "nipa-disc-" + Guid.NewGuid().ToString("N"));
            var project = Path.Combine(tmp, "game");
            Directory.CreateDirectory(Path.Combine(tmp, ".nipa"));
            Directory.CreateDirectory(project);
            try
            {
                Assert.That(ProjectPaths.Discover(project), Is.Null);
                File.WriteAllText(Path.Combine(tmp, ".nipa", "config"), "{}");
                var found = ProjectPaths.Discover(project);
                Assert.That(found.CloneRoot, Is.EqualTo(Path.GetFullPath(tmp)));
                Assert.That(found.Prefix, Is.EqualTo("game"));
            }
            finally
            {
                Directory.Delete(tmp, true);
            }
        }

        [Test]
        public void MetaPairing()
        {
            Assert.That(ProjectPaths.IsMeta("a.png.meta"), Is.True);
            Assert.That(ProjectPaths.IsMeta("a.png"), Is.False);
            Assert.That(ProjectPaths.MetaOf("a.png"), Is.EqualTo("a.png.meta"));
            Assert.That(ProjectPaths.MetaOf("a.png.meta"), Is.EqualTo("a.png.meta"));
            Assert.That(ProjectPaths.AssetOf("a.png.meta"), Is.EqualTo("a.png"));
            Assert.That(ProjectPaths.AssetOf("a.png"), Is.EqualTo("a.png"));
            Assert.That(ProjectPaths.Counterpart("a.png"), Is.EqualTo("a.png.meta"));
            Assert.That(ProjectPaths.Counterpart("a.png.meta"), Is.EqualTo("a.png"));
            Assert.That(ProjectPaths.Pair("a.png.meta").ToArray(), Is.EqualTo(new[] { "a.png", "a.png.meta" }));
            Assert.That(ProjectPaths.Normalize(null), Is.EqualTo(""));
        }
    }

    [TestFixture]
    public class StatusCacheTests
    {
        static StatusResponse S(Action<StatusResponse> f)
        {
            var s = new StatusResponse { Branch = "main" };
            f(s);
            return s;
        }

        [Test]
        public void CombinesFlagsAndAggregatesDirectories()
        {
            var c = new StatusCache();
            c.Apply(S(s =>
            {
                s.Staged = new List<string> { "Assets/a.png" };
                s.Added = new List<string> { "Assets/a.png" };
                s.Modified = new List<string> { "Assets/Sub/b.prefab" };
                s.Deleted = new List<string> { "Assets/c" };
                s.Untracked = new List<string> { "/Assets/u.txt" };
                s.Missing = new List<string> { "m" };
                s.Conflicts = new List<string> { "Assets/x" };
                s.Resolved = new List<string> { "Assets/x" };
            }));
            Assert.That(c.Get("Assets/a.png"), Is.EqualTo(FileState.Staged | FileState.Added));
            Assert.That(c.Get("Assets/u.txt"), Is.EqualTo(FileState.Untracked));
            Assert.That(c.Get("Assets/x"), Is.EqualTo(FileState.Conflict | FileState.Resolved));
            Assert.That(c.Get("Assets/Sub"), Is.EqualTo(FileState.Modified));
            Assert.That(c.Get("Assets").HasFlag(FileState.Modified | FileState.Staged | FileState.Conflict), Is.True);
            Assert.That(c.Get("nothing"), Is.EqualTo(FileState.None));
            Assert.That(c.Get(null), Is.EqualTo(FileState.None));
            Assert.That(c.Has("Assets/a.png"), Is.True);
            Assert.That(c.Has("Assets"), Is.False);
            Assert.That(c.Snapshot().Count, Is.EqualTo(6));
        }

        [Test]
        public void ReportsOnlyChangedPaths()
        {
            var c = new StatusCache();
            IReadOnlyCollection<string> fired = null;
            c.Changed += p => fired = p;
            var first = c.Apply(S(s => s.Modified = new List<string> { "a/x", "a/y" }));
            Assert.That(first, Is.SupersetOf(new[] { "a/x", "a/y", "a" }));
            Assert.That(fired, Is.Not.Null);

            fired = null;
            Assert.That(c.Apply(S(s => s.Modified = new List<string> { "a/x", "a/y" })), Is.Empty);
            Assert.That(fired, Is.Null);

            var delta = c.Apply(S(s => { s.Modified = new List<string> { "a/x" }; s.Staged = new List<string> { "a/y" }; }));
            Assert.That(delta, Is.SupersetOf(new[] { "a/y", "a" }));
            Assert.That(delta, Does.Not.Contain("a/x"));
        }

        [Test]
        public void BranchHeadAndPendingTrigger()
        {
            var c = new StatusCache();
            c.Apply(S(_ => { }));
            var d = c.Apply(new StatusResponse { Branch = "dev", Head = new StatusHead { Kind = "tag", Name = "v1" }, Pending = "merge", PendingRemaining = 2 });
            Assert.That(d, Does.Contain(""));
            Assert.That((c.Branch, c.Head.Name, c.Pending, c.PendingRemaining), Is.EqualTo(("dev", "v1", "merge", 2)));
            Assert.That(c.Apply(new StatusResponse { Branch = "dev", Head = new StatusHead { Kind = "tag", Name = "v1" }, Pending = "merge", PendingRemaining = 2 }), Is.Empty);
        }

        [Test]
        public void RemovedPathsAreReported()
        {
            var c = new StatusCache();
            c.Apply(S(s => s.Modified = new List<string> { "d/e/f" }));
            var gone = c.Apply(S(_ => { }));
            Assert.That(gone, Is.SupersetOf(new[] { "d/e/f", "d/e", "d" }));
            Assert.That(c.Get("d"), Is.EqualTo(FileState.None));
        }
    }

    [TestFixture]
    public class SettingsTests
    {
        [Test]
        public void RoundTripAndClamp()
        {
            var dir = Path.Combine(Path.GetTempPath(), "nipa-set-" + Guid.NewGuid().ToString("N"));
            var path = Path.Combine(dir, "sub", "NipaSettings.json");
            try
            {
                Assert.That(NipaSettings.Load(path).PollIntervalSeconds, Is.EqualTo(60));
                new NipaSettings { PollIntervalSeconds = 5 }.Save(path);
                Assert.That(NipaSettings.Load(path).PollIntervalSeconds, Is.EqualTo(15));
                new NipaSettings { PollIntervalSeconds = 99999 }.Save(path);
                Assert.That(NipaSettings.Load(path).PollIntervalSeconds, Is.EqualTo(3600));
                new NipaSettings { PollIntervalSeconds = 120 }.Save(path);
                Assert.That(NipaSettings.Load(path).PollIntervalSeconds, Is.EqualTo(120));
                File.WriteAllText(path, "{bad");
                Assert.That(NipaSettings.Load(path).PollIntervalSeconds, Is.EqualTo(60));
                File.WriteAllText(path, "null");
                Assert.That(NipaSettings.Load(path).PollIntervalSeconds, Is.EqualTo(60));
            }
            finally
            {
                if (Directory.Exists(dir))
                    Directory.Delete(dir, true);
            }
        }
    }

    [TestFixture]
    public class SessionTests
    {
        FakeDaemon fake;
        NipaSession session;
        string root;

        [SetUp]
        public void SetUp()
        {
            fake = new FakeDaemon();
            root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "nipa-clone"));
            var paths = new ProjectPaths(Path.Combine(root, "game"), root);
            session = new NipaSession(new DaemonClient(fake.Endpoint()), paths) { ReconnectDelay = TimeSpan.FromMilliseconds(20) };
        }

        [TearDown]
        public void TearDown()
        {
            session.Dispose();
            fake.Dispose();
        }

        [Test]
        public async Task StartWatchesRepoLoadsStatusAndRefreshesOnChange()
        {
            var status = "{\"modified\":[\"game/Assets/a.png\"],\"branch\":\"main\"}";
            fake.On("WatchRepo", "{}");
            fake.Handlers["Status"] = _ => new Reply { Body = status };
            using var gate = new ManualResetEventSlim();
            fake.On("WatchChanges", new Reply { Lines = new[] { "{\"paths\":[]}" }, Hold = true, Gate = gate });
            await session.StartAsync();
            Assert.That(session.Cache.Get("game/Assets/a.png"), Is.EqualTo(FileState.Modified));
            Assert.That(fake.Requests.First().Body, Does.Contain(root.Replace("\\", "\\\\")));
            status = "{\"staged\":[\"game/Assets/a.png\"],\"branch\":\"main\"}";
            gate.Set();
            Assert.That(SpinWait.SpinUntil(() => session.Cache.Get("game/Assets/a.png") == FileState.Staged, 3000), Is.True);
        }

        [Test]
        public async Task WatchLoopReconnectsAndReportsFailure()
        {
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{\"branch\":\"main\"}");
            var calls = 0;
            fake.Handlers["WatchChanges"] = _ =>
            {
                Interlocked.Increment(ref calls);
                return new Reply { Status = 500, Body = "{\"message\":\"boom\"}" };
            };
            Exception failure = null;
            session.WatchFailed += e => failure = e;
            await session.StartAsync();
            Assert.That(SpinWait.SpinUntil(() => Volatile.Read(ref calls) >= 2, 3000), Is.True);
            Assert.That(failure, Is.InstanceOf<DaemonException>());
        }

        [Test]
        public async Task StageExpandsMetaPairsKnownToCache()
        {
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{\"modified\":[\"game/Assets/a.png\",\"game/Assets/a.png.meta\",\"game/Assets/b.png\"]}");
            fake.On("WatchChanges", new Reply { Lines = new string[0], Hold = true });
            fake.On("Stage", "{\"staged\":[\"game/Assets/a.png\",\"game/Assets/a.png.meta\"]}");
            await session.StartAsync();
            await session.StageAsync(new[] { "game/Assets/a.png", "game/Assets/b.png" });
            var req = fake.Requests.Last(r => r.Method == "Stage");
            Assert.That(req.Body, Does.Contain("\"game/Assets/a.png\",\"game/Assets/a.png.meta\",\"game/Assets/b.png\""));
            Assert.That(req.Body, Does.Not.Contain("b.png.meta"));
            Assert.That(session.Cache.Get("game/Assets/a.png.meta").HasFlag(FileState.Staged), Is.True);
        }

        [Test]
        public async Task UnstageAndRestoreExpandFromMeta()
        {
            const string st = "{\"staged\":[\"x/a.png\",\"x/a.png.meta\"]}";
            fake.On("Stage", st);
            fake.On("Restore", st);
            fake.On("Status", st);
            await session.RefreshAsync();
            await session.UnstageAsync(new[] { "x/a.png.meta" });
            await session.RestoreAsync(new[] { "x/a.png", "x/a.png", "" });
            var stage = fake.Requests.Last(r => r.Method == "Stage").Body;
            Assert.That(stage, Does.Contain("\"unstage\":[\"x/a.png.meta\",\"x/a.png\"]"));
            Assert.That(fake.Requests.Last(r => r.Method == "Restore").Body, Does.Contain("\"paths\":[\"x/a.png\",\"x/a.png.meta\"]"));
        }

        [Test]
        public async Task PushUpdateSwitchRunOpsAndRefresh()
        {
            fake.On("Status", "{\"branch\":\"main\"}");
            fake.On("Push", new Reply { Lines = new[] { "{\"result\":{\"push\":{\"commit_id\":\"c1\"}}}" } });
            fake.On("Update", new Reply { Lines = new[] { "{\"result\":{\"sync\":{\"branch\":\"main\"}}}" } });
            fake.On("Switch", new Reply { Lines = new[] { "{\"result\":{\"sync\":{\"branch\":\"dev\"}}}" } });
            var seen = new List<OpEvent>();
            Assert.That((await session.PushAsync("msg", seen.Add)).Push.CommitId, Is.EqualTo("c1"));
            Assert.That((await session.UpdateAsync(null)).Sync.Branch, Is.EqualTo("main"));
            Assert.That((await session.SwitchAsync("dev", null, null)).Sync.Branch, Is.EqualTo("dev"));
            Assert.That(seen.Count, Is.EqualTo(1));
            Assert.That(fake.Requests.Count(r => r.Method == "Status"), Is.EqualTo(3));
            Assert.That(fake.Requests.First(r => r.Method == "Push").Body, Does.Contain("\"message\":\"msg\""));
        }

        [Test]
        public async Task MetaWarningsHandlesNull()
        {
            fake.On("MetaWarnings", "{\"warnings\":[{\"path\":\"a\",\"kind\":\"missing-meta\"}]}");
            Assert.That((await session.MetaWarningsAsync()).Single().Kind, Is.EqualTo("missing-meta"));
            fake.On("MetaWarnings", "{}");
            Assert.That(await session.MetaWarningsAsync(), Is.Empty);
        }

        [TestCase("http://localhost:8080/acme/game", "nipa://localhost:8080/acme/game/changes")]
        [TestCase("nipa.example.com/acme/game/", "nipa://nipa.example.com/acme/game/changes")]
        public async Task DesktopLinkStripsScheme(string url, string expected)
        {
            Assert.That(session.DesktopLink("changes"), Is.Null);
            fake.On("WatchRepo", "{\"repo\":{\"url\":\"" + url + "\"}}");
            fake.On("Status", "{}");
            fake.On("WatchChanges", new Reply { Lines = new string[0], Hold = true });
            await session.StartAsync();
            Assert.That(session.DesktopLink("changes"), Is.EqualTo(expected));
        }

        [Test]
        public async Task WatchLoopReconnectsToRestartedDaemon()
        {
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{\"branch\":\"old\"}");
            fake.On("WatchChanges", new Reply { Status = 500, Body = "{\"message\":\"gone\"}" });
            using var second = new FakeDaemon();
            second.On("WatchRepo", "{}");
            second.On("Status", "{\"branch\":\"new\"}");
            second.On("WatchChanges", new Reply { Lines = new string[0], Hold = true });
            var calls = 0;
            var resilient = new NipaSession(new DaemonClient(fake.Endpoint()), session.Paths, _ =>
            {
                Interlocked.Increment(ref calls);
                return Task.FromResult(new DaemonClient(second.Endpoint("fresh")));
            }) { ReconnectDelay = TimeSpan.FromMilliseconds(20) };
            try
            {
                await resilient.StartAsync();
                Assert.That(SpinWait.SpinUntil(() => resilient.Cache.Branch == "new", 3000), Is.True);
                Assert.That(second.Requests.Any(r => r.Method == "WatchRepo" && r.Token == "fresh"), Is.True);
                Assert.That(calls, Is.EqualTo(1));
            }
            finally
            {
                resilient.Dispose();
            }
        }

        [Test]
        public async Task ReconnectFailureIsReportedAndRetried()
        {
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{}");
            fake.On("WatchChanges", new Reply { Status = 500, Body = "{}" });
            var calls = 0;
            var failures = 0;
            var s = new NipaSession(new DaemonClient(fake.Endpoint()), session.Paths, _ =>
            {
                Interlocked.Increment(ref calls);
                throw new DaemonException(503, "daemon unavailable");
            }) { ReconnectDelay = TimeSpan.FromMilliseconds(10) };
            s.WatchFailed += _ => Interlocked.Increment(ref failures);
            try
            {
                await s.StartAsync();
                Assert.That(SpinWait.SpinUntil(() => Volatile.Read(ref calls) >= 2, 3000), Is.True);
                Assert.That(failures, Is.GreaterThanOrEqualTo(3));
            }
            finally
            {
                s.Dispose();
            }
        }

        [Test]
        public async Task RestorableSkipsUntrackedStagedAndUnchanged()
        {
            fake.On("Status", "{\"modified\":[\"a/mod.png\",\"a/metaonly.png.meta\",\"a/dir/x\"],\"deleted\":[\"a/del.png\"],"
                + "\"untracked\":[\"a/new.png\"],\"staged\":[\"a/stg.png\"],\"added\":[\"a/add.png\"],\"missing\":[\"a/miss.png\"]}");
            await session.RefreshAsync();
            var got = session.Restorable(new[] { "a/mod.png", "a/metaonly.png", "a/del.png", "a/new.png", "a/stg.png", "a/add.png", "a/miss.png", "a/clean.png", "a/dir", "" });
            Assert.That(got, Is.EqualTo(new[] { "a/mod.png", "a/metaonly.png", "a/del.png", "a/miss.png", "a/dir/x" }));
        }

        [Test]
        public async Task RestorableExpandsDirectoriesAroundStagedChildren()
        {
            fake.On("Status", "{\"modified\":[\"d/a.png\",\"d/sub/b.png\",\"d/sub/b.png.meta\",\"e/x\"],"
                + "\"staged\":[\"d/staged.png\"],\"untracked\":[\"d/new.png\"]}");
            await session.RefreshAsync();
            Assert.That(session.Restorable(new[] { "d" }), Is.EqualTo(new[] { "d/a.png", "d/sub/b.png", "d/sub/b.png.meta" }));
            Assert.That(session.Restorable(new[] { "d/sub", "d/sub/b.png" }), Is.EqualTo(new[] { "d/sub/b.png", "d/sub/b.png.meta" }));
            Assert.That(session.Restorable(new[] { "e", "e/x", "nothing" }), Is.EqualTo(new[] { "e/x" }));
            Assert.That(session.Restorable(new[] { "d/new.png" }), Is.Empty);
        }

        [Test]
        public void StopWithoutStartIsSafe()
        {
            session.Stop();
            Assert.That(session.Root, Is.EqualTo(Path.GetFullPath(root)));
        }
    }
}
