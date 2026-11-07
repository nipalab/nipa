using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Session;
using Nipa.Editor.Core.Status;
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    [TestFixture]
    public class LockPolicyTests
    {
        static FileLockDetail L(string path, string by, string name, bool global = true, string branch = "") =>
            new FileLockDetail { Path = path, HeldBy = by, HeldByName = name, Global = global, Branch = branch };

        static LockPolicy Policy(bool defaultBranch, string branch, bool strict, params FileLockDetail[] locks) =>
            new LockPolicy(new LockStatusResponse
            {
                UserId = "me",
                Branch = branch,
                DefaultBranch = defaultBranch,
                Locks = locks.ToList(),
            }, strict, null);

        [Test]
        public void CoversExactAndPrefix()
        {
            Assert.That(LockPolicy.Covers("a/b.png", "a/b.png"), Is.True);
            Assert.That(LockPolicy.Covers("a/", "a/b/c.png"), Is.True);
            Assert.That(LockPolicy.Covers("a", "ab/c.png"), Is.False);
            Assert.That(LockPolicy.Covers("a/b", "a"), Is.False);
            Assert.That(LockPolicy.Covers("", "a"), Is.False);
            Assert.That(LockPolicy.Covers("a\\b", "a/b/c"), Is.True);
        }

        [Test]
        public void ScopeFollowsBranch()
        {
            var global = L("a.png", "bob", "Bob");
            var branchLock = L("b.png", "bob", "Bob", false, "feat");
            var onMain = Policy(true, "main", false, global, branchLock);
            Assert.That(onMain.Lookup("a.png").State, Is.EqualTo(LockState.Theirs));
            Assert.That(onMain.Lookup("b.png").State, Is.EqualTo(LockState.Free));
            var onFeat = Policy(false, "feat", false, global, branchLock);
            Assert.That(onFeat.Lookup("a.png").State, Is.EqualTo(LockState.Free));
            Assert.That(onFeat.Lookup("b.png").State, Is.EqualTo(LockState.Theirs));
            Assert.That(onFeat.LockBranch, Is.EqualTo("feat"));
            Assert.That(onMain.LockBranch, Is.EqualTo(""));
        }

        [Test]
        public void MetaAndAssetShareState()
        {
            var p = Policy(true, "main", false, L("Assets/a.prefab.meta", "bob", "Bob"));
            Assert.That(p.Lookup("Assets/a.prefab").Holder, Is.EqualTo("Bob"));
            Assert.That(p.Lookup("Assets/a.prefab.meta").State, Is.EqualTo(LockState.Theirs));
            var mine = Policy(true, "main", false, L("Assets/a.prefab", "me", "Me"));
            Assert.That(mine.Lookup("Assets/a.prefab.meta").State, Is.EqualTo(LockState.Mine));
            var both = Policy(true, "main", false, L("Assets/a.prefab", "me", "Me"), L("Assets/a.prefab.meta", "bob", ""));
            var info = both.Lookup("Assets/a.prefab");
            Assert.That(info.State, Is.EqualTo(LockState.Theirs));
            Assert.That(info.Holder, Is.EqualTo("bob"));
        }

        [Test]
        public void DirectoryLockCovers()
        {
            var p = Policy(true, "main", false, L("Assets/Scenes", "bob", "Bob"));
            Assert.That(p.Lookup("Assets/Scenes/x.unity").State, Is.EqualTo(LockState.Theirs));
            Assert.That(p.Lookup("Assets/Scenes2/x.unity").State, Is.EqualTo(LockState.Free));
        }

        [Test]
        public void OpenForEditRules()
        {
            var p = Policy(true, "main", true, L("Assets/b.prefab", "bob", "Bob"), L("Assets/mine.prefab", "me", "Me"));
            Assert.That(p.IsOpenForEdit("Assets/b.prefab", false, out var msg), Is.False);
            Assert.That(msg, Does.Contain("Bob"));
            Assert.That(p.IsOpenForEdit("Assets/mine.prefab", false, out msg), Is.True);
            Assert.That(msg, Is.Null);
            Assert.That(p.IsOpenForEdit("Assets/free.prefab", false, out msg), Is.False);
            Assert.That(msg, Does.Contain("locked before"));
            Assert.That(p.IsOpenForEdit("Assets/free.prefab", true, out _), Is.True);
            Assert.That(p.IsOpenForEdit("Assets/free.cs", false, out _), Is.True);
            Assert.That(p.IsOpenForEdit("Assets/free.PREFAB.meta", false, out _), Is.False);
            Assert.That(p.IsOpenForEdit("Assets/.hidden", false, out _), Is.True);
            Assert.That(p.RequiresLock("Assets/noext"), Is.False);
            var loose = Policy(true, "main", false);
            Assert.That(loose.IsOpenForEdit("Assets/free.prefab", false, out _), Is.True);
        }

        [Test]
        public void CustomExtensionsNormalize()
        {
            var p = new LockPolicy(new LockStatusResponse(), true, new[] { "xyz", ".Abc", " " });
            Assert.That(p.RequiresLock("a.XYZ"), Is.True);
            Assert.That(p.RequiresLock("a.abc"), Is.True);
            Assert.That(p.RequiresLock("a.prefab"), Is.False);
        }

        [Test]
        public void CanChangeBlocksTheirs()
        {
            var p = Policy(true, "main", false, L("Assets/a.png", "bob", "Bob"));
            Assert.That(p.CanChange("Assets/a.png", out var msg), Is.False);
            Assert.That(msg, Does.Contain("Bob"));
            Assert.That(p.CanChange("Assets/b.png", out msg), Is.True);
            Assert.That(msg, Is.Null);
        }

        [Test]
        public void ReleaseRights()
        {
            var bob = L("a", "bob", "Bob");
            var me = L("b", "me", "Me");
            var p = Policy(true, "main", false, bob, me);
            Assert.That(p.CanRelease(bob), Is.False);
            Assert.That(p.CanRelease(me), Is.True);
            Assert.That(p.Mine.Select(l => l.Path), Is.EqualTo(new[] { "b" }));
            var admin = new LockPolicy(new LockStatusResponse { UserId = "me", CanReleaseAny = true, DefaultBranch = true, Locks = new List<FileLockDetail> { bob } }, false, null);
            Assert.That(admin.CanRelease(bob), Is.True);
            Assert.That(LockPolicy.Empty.IsMine(me), Is.False);
        }

        [Test]
        public void CheckPushSplitsNeededAndTheirs()
        {
            var p = new LockPolicy(new LockStatusResponse
            {
                Files = new List<FileLockState>
                {
                    new FileLockState { Path = "/a.png", State = "needed" },
                    new FileLockState { Path = "a.png", State = "needed" },
                    new FileLockState { Path = "b.png", State = "theirs", HeldByName = "Bob" },
                    new FileLockState { Path = "c.png", State = "mine" },
                    new FileLockState { Path = "d.png", State = "free" },
                },
            }, false, null);
            var check = p.CheckPush();
            Assert.That(check.Needed, Is.EqualTo(new[] { "a.png" }));
            Assert.That(check.Theirs.Single().HeldByName, Is.EqualTo("Bob"));
            Assert.That(check.Blocked, Is.True);
            Assert.That(LockPolicy.Empty.CheckPush().Blocked, Is.False);
        }

        [Test]
        public void SettingsDefaultsAndNormalize()
        {
            var tmp = Path.Combine(Path.GetTempPath(), "nipa-set-" + Guid.NewGuid().ToString("N"), "s.json");
            try
            {
                var s = new NipaSettings { LockStrict = true, LockExtensions = null };
                s.Save(tmp);
                var loaded = NipaSettings.Load(tmp);
                Assert.That(loaded.LockStrict, Is.True);
                Assert.That(loaded.LockExtensions, Does.Contain(".prefab"));
                Assert.That(loaded.LockExtensions, Does.Contain(".unity"));
            }
            finally
            {
                Directory.Delete(Path.GetDirectoryName(tmp), true);
            }
        }
    }

    [TestFixture]
    public class SessionLockTests
    {
        FakeDaemon fake;
        NipaSession session;
        string lockJson;

        [SetUp]
        public void SetUp()
        {
            fake = new FakeDaemon();
            var root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "nipa-lock-clone"));
            session = new NipaSession(new DaemonClient(fake.Endpoint()), new ProjectPaths(Path.Combine(root, "game"), root))
            {
                ReconnectDelay = TimeSpan.FromMilliseconds(20),
            };
            lockJson = "{\"user_id\":\"me\",\"branch\":\"main\",\"default_branch\":true,\"locks\":[{\"path\":\"game/Assets/b.prefab\",\"held_by\":\"bob\",\"held_by_name\":\"Bob\",\"global\":true}]}";
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{\"branch\":\"main\",\"untracked\":[\"game/Assets/new.prefab\"]}");
            fake.Handlers["LockStatus"] = _ => new Reply { Body = lockJson };
            fake.On("WatchChanges", new Reply { Lines = new string[0], Hold = true });
            fake.On("WatchEvents", new Reply { Lines = new string[0], Hold = true });
        }

        [TearDown]
        public void TearDown()
        {
            session.Dispose();
            fake.Dispose();
        }

        int Calls(string method) => fake.Requests.Count(r => r.Method == method);

        [Test]
        public async Task StartLoadsLocksAndAppliesStrictSettings()
        {
            var changed = 0;
            session.LocksChanged += () => Interlocked.Increment(ref changed);
            await session.StartAsync();
            Assert.That(session.Locks.Lookup("game/Assets/b.prefab").State, Is.EqualTo(LockState.Theirs));
            Assert.That(session.IsOpenForEdit("game/Assets/b.prefab", out var msg), Is.False);
            Assert.That(msg, Does.Contain("Bob"));
            Assert.That(session.IsOpenForEdit("game/Assets/a.prefab", out _), Is.True);

            session.ApplySettings(new NipaSettings { LockStrict = true });
            Assert.That(session.IsOpenForEdit("game/Assets/a.prefab", out msg), Is.False);
            Assert.That(session.IsOpenForEdit("game/Assets/new.prefab", out _), Is.True);
            Assert.That(changed, Is.GreaterThanOrEqualTo(2));
            session.ApplySettings(null);
            Assert.That(session.Locks.Strict, Is.False);
        }

        [Test]
        public async Task LockFailureOnLockStatusDoesNotFailStart()
        {
            fake.On("LockStatus", new Reply { Status = 500, Body = "{\"message\":\"nope\"}" });
            Exception seen = null;
            session.WatchFailed += e => seen = e;
            await session.StartAsync();
            Assert.That(seen, Is.Not.Null);
            Assert.That(session.Locks, Is.SameAs(LockPolicy.Empty));
        }

        [Test]
        public async Task LockPairsAssetAndMetaWithBranchScope()
        {
            await session.StartAsync();
            fake.On("ProxyLockFile", "{}");
            lockJson = lockJson.Replace("\"locks\":[", "\"locks\":[{\"path\":\"game/Assets/a.png\",\"held_by\":\"me\",\"global\":true},");
            var result = await session.LockAsync(new[] { "game/Assets/a.png.meta", "game/Assets/a.png", "game/Assets/mine.png" });
            Assert.That(result.Failed, Is.Empty);
            Assert.That(result.Locked, Is.EqualTo(new[] { "game/Assets/a.png", "game/Assets/mine.png" }));
            var bodies = fake.Requests.Where(r => r.Method == "ProxyLockFile").Select(r => r.Body).ToList();
            Assert.That(bodies, Has.Count.EqualTo(4));
            Assert.That(bodies[0], Does.Contain("game/Assets/a.png\""));
            Assert.That(bodies[1], Does.Contain("game/Assets/a.png.meta"));
            Assert.That(session.Locks.Lookup("game/Assets/a.png").State, Is.EqualTo(LockState.Mine));
        }

        [Test]
        public async Task LockOnFeatureBranchSendsBranch()
        {
            lockJson = "{\"user_id\":\"me\",\"branch\":\"feat\",\"default_branch\":false}";
            await session.StartAsync();
            fake.On("ProxyLockFile", "{}");
            await session.LockAsync(new[] { "game/Assets/a.png" });
            Assert.That(fake.Requests.First(r => r.Method == "ProxyLockFile").Body, Does.Contain("\"branch\":\"feat\""));
        }

        [Test]
        public async Task LockConflictReportsHolderAndSkipsMeta()
        {
            await session.StartAsync();
            fake.On("ProxyLockFile", new Reply { Status = 409, Body = "{\"code\":\"AlreadyExists\",\"message\":\"locked by Bob\"}" });
            var result = await session.LockAsync(new[] { "game/Assets/b.prefab" });
            Assert.That(result.Locked, Is.Empty);
            var f = result.Failed.Single();
            Assert.That(f.Holder, Is.EqualTo("Bob"));
            Assert.That(f.Message, Does.Contain("locked by Bob"));
            Assert.That(Calls("ProxyLockFile"), Is.EqualTo(1));
        }

        [Test]
        public async Task LockFailureWithoutHolderKeepsMessage()
        {
            await session.StartAsync();
            fake.On("ProxyLockFile", new Reply { Status = 403, Body = "{\"message\":\"denied\"}" });
            var result = await session.LockAsync(new[] { "game/Assets/free.png" });
            Assert.That(result.Failed.Single().Holder, Is.Null);
        }

        [Test]
        public async Task UnlockReleasesOnlyMineWithTheirOwnScope()
        {
            lockJson = "{\"user_id\":\"me\",\"branch\":\"feat\",\"default_branch\":false,\"locks\":["
                + "{\"path\":\"game/Assets/a.png\",\"held_by\":\"me\",\"branch\":\"feat\"},"
                + "{\"path\":\"game/Assets/a.png.meta\",\"held_by\":\"me\",\"branch\":\"feat\"},"
                + "{\"path\":\"game/Assets/b.png\",\"held_by\":\"bob\",\"branch\":\"feat\"}]}";
            await session.StartAsync();
            fake.Handlers["ProxyUnlockFile"] = r => r.Body.Contains(".meta")
                ? new Reply { Status = 404, Body = "{\"message\":\"gone\"}" }
                : new Reply { Body = "{}" };
            await session.UnlockAsync(new[] { "game/Assets/a.png", "game/Assets/b.png" });
            var bodies = fake.Requests.Where(r => r.Method == "ProxyUnlockFile").Select(r => r.Body).ToList();
            Assert.That(bodies, Has.Count.EqualTo(2));
            Assert.That(bodies, Has.All.Contain("\"branch\":\"feat\""));
            Assert.That(bodies.Any(b => b.Contains("b.png")), Is.False);
        }

        [Test]
        public async Task ReleaseAnyUsesGlobalAsEmptyBranch()
        {
            await session.StartAsync();
            fake.On("ProxyUnlockFile", "{}");
            await session.ReleaseAsync(session.Locks.Locks.Single());
            var body = fake.Requests.First(r => r.Method == "ProxyUnlockFile").Body;
            Assert.That(body, Does.Contain("\"branch\":\"\""));
            Assert.That(body, Does.Contain("game/Assets/b.prefab"));
        }

        [Test]
        public async Task ReleaseErrorPropagatesButRefreshes()
        {
            await session.StartAsync();
            var before = Calls("LockStatus");
            fake.On("ProxyUnlockFile", new Reply { Status = 403, Body = "{\"message\":\"no\"}" });
            Assert.ThrowsAsync<DaemonException>(() => session.ReleaseAsync(session.Locks.Locks.Single()));
            Assert.That(Calls("LockStatus"), Is.EqualTo(before + 1));
        }

        [Test]
        public async Task CheckPushRefreshesAndReturnsBlockers()
        {
            await session.StartAsync();
            lockJson = "{\"user_id\":\"me\",\"files\":[{\"path\":\"x.png\",\"state\":\"needed\"},{\"path\":\"y.png\",\"state\":\"theirs\",\"held_by_name\":\"Bob\"}]}";
            var check = await session.CheckPushAsync();
            Assert.That(check.Needed, Is.EqualTo(new[] { "x.png" }));
            Assert.That(check.Theirs, Has.Count.EqualTo(1));
        }

        [Test]
        public async Task WritablePathsDropsLockedByOthers()
        {
            await session.StartAsync();
            var allowed = session.WritablePaths(new[] { "game/Assets/a.png", "game/Assets/b.prefab" }, out var rejected);
            Assert.That(allowed, Is.EqualTo(new[] { "game/Assets/a.png" }));
            Assert.That(rejected.Single(), Does.Contain("Bob"));
            session.ApplySettings(new NipaSettings { LockStrict = true });
            allowed = session.WritablePaths(new[] { "game/Assets/free.prefab" }, out rejected);
            Assert.That(allowed, Is.EqualTo(new[] { "game/Assets/free.prefab" }));
            Assert.That(rejected, Is.Empty);
        }

        [Test]
        public async Task ActivityTracksCountsAndRefreshesLocksOnRelease()
        {
            fake.On("WatchEvents", new Reply
            {
                Lines = new[]
                {
                    "{\"incoming\":2,\"review_requests\":1,\"events\":[{\"kind\":\"pushed\",\"actor\":\"Bob\",\"count\":2}]}",
                    "{\"incoming\":2,\"events\":[{\"kind\":\"lock_released\",\"path\":\"game/Assets/b.prefab\"}]}",
                },
                Hold = true,
            });
            var seen = new List<RepoActivity>();
            session.Activity += a => { lock (seen) seen.Add(a); };
            await session.StartAsync();
            Assert.That(SpinWait.SpinUntil(() => { lock (seen) return seen.Count == 2; }, 3000), Is.True);
            Assert.That(session.Incoming, Is.EqualTo(2));
            Assert.That(session.ReviewRequests, Is.EqualTo(0));
            Assert.That(Calls("LockStatus"), Is.GreaterThanOrEqualTo(2));
            Assert.That(fake.Requests.First(r => r.Method == "WatchEvents").Body, Does.Contain("\"interval_seconds\":0"));
        }

        [Test]
        public async Task ActivityStreamFailureRetries()
        {
            var calls = 0;
            fake.Handlers["WatchEvents"] = _ =>
            {
                Interlocked.Increment(ref calls);
                return new Reply { Status = 500, Body = "{\"message\":\"boom\"}" };
            };
            await session.StartAsync();
            Assert.That(SpinWait.SpinUntil(() => Volatile.Read(ref calls) >= 2, 3000), Is.True);
        }

        [Test]
        public async Task OpRefreshesLocksAfterwards()
        {
            await session.StartAsync();
            fake.On("Update", new Reply { Lines = new[] { "{\"result\":{\"sync\":{\"branch\":\"main\"}}}" } });
            var before = Calls("LockStatus");
            await session.UpdateAsync(null);
            Assert.That(Calls("LockStatus"), Is.EqualTo(before + 1));
        }

        [Test]
        public async Task PollRefreshesLocks()
        {
            session.PollInterval = TimeSpan.FromMilliseconds(20);
            await session.StartAsync();
            Assert.That(SpinWait.SpinUntil(() => Calls("LockStatus") >= 3, 3000), Is.True);
        }
    }
}
