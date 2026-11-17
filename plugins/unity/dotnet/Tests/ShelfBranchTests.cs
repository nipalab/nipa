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
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    [TestFixture]
    public class LockWatchesTests
    {
        [Test]
        public void ParseNormalizesAndDedupes()
        {
            var w = LockWatches.Parse("a/b.png\n a/b.png.meta \n\\c\\d.prefab\n\n");
            Assert.That(w.Paths, Is.EqualTo(new[] { "a/b.png", "c/d.prefab" }));
            Assert.That(LockWatches.Parse(w.Serialize()).Paths, Is.EqualTo(w.Paths));
            Assert.That(LockWatches.Parse(null).Paths, Is.Empty);
        }

        [Test]
        public void WatchAndUnwatchRaiseChanged()
        {
            var w = new LockWatches();
            var changed = 0;
            w.Changed += () => changed++;
            w.Watch("a/b.png.meta");
            w.Watch("a/b.png");
            w.Watch(" ");
            Assert.That(w.Paths, Is.EqualTo(new[] { "a/b.png" }));
            Assert.That(w.IsWatched("a/b.png.meta"), Is.True);
            w.Unwatch("x");
            w.Unwatch("a/b.png");
            Assert.That(w.Paths, Is.Empty);
            Assert.That(changed, Is.EqualTo(2));
        }

        [Test]
        public void HitsMatchExactFolderAndMeta()
        {
            var watched = new[] { "Assets/a.png", "Assets/Scenes/x.unity", "Assets/b.prefab" };
            Assert.That(LockWatches.Hits(watched, "Assets/a.png"), Is.EqualTo(new[] { "Assets/a.png" }));
            Assert.That(LockWatches.Hits(watched, "/Assets/Scenes/"), Is.EqualTo(new[] { "Assets/Scenes/x.unity" }));
            Assert.That(LockWatches.Hits(watched, "Assets/b.prefab.meta"), Is.EqualTo(new[] { "Assets/b.prefab" }));
            Assert.That(LockWatches.Hits(watched, "Assets/ab.png"), Is.Empty);
            Assert.That(LockWatches.Hits(watched, ""), Is.Empty);
            Assert.That(LockWatches.Hits(watched, null), Is.Empty);
        }

        [Test]
        public void TakeHitsRemovesThem()
        {
            var w = LockWatches.Parse("Assets/a.png\nAssets/b.png");
            var changed = 0;
            w.Changed += () => changed++;
            Assert.That(w.TakeHits("Assets/c.png"), Is.Empty);
            Assert.That(changed, Is.EqualTo(0));
            Assert.That(w.TakeHits("Assets/a.png"), Is.EqualTo(new[] { "Assets/a.png" }));
            Assert.That(w.Paths, Is.EqualTo(new[] { "Assets/b.png" }));
            Assert.That(changed, Is.EqualTo(1));
        }
    }

    [TestFixture]
    public class OpSummaryTests
    {
        [Test]
        public void DescribesMergeOutcomes()
        {
            Assert.That(OpSummary.Describe((OpResult)null), Is.Null);
            Assert.That(OpSummary.Describe(new OpResult()), Is.Null);
            Assert.That(OpSummary.Describe(new OpResult { Merge = new MergeResult { Conflicts = new List<string> { "a", "b" } } }), Does.StartWith("2 conflict"));
            Assert.That(OpSummary.Describe(new OpResult { Merge = new MergeResult { Aborted = true } }), Is.EqualTo("merge aborted"));
            Assert.That(OpSummary.Describe(new OpResult { Merge = new MergeResult { UpToDate = true } }), Is.EqualTo("already up to date"));
            Assert.That(OpSummary.Describe(new OpResult { Merge = new MergeResult { FastForwarded = true } }), Is.EqualTo("fast-forwarded"));
            Assert.That(OpSummary.Describe(new OpResult { Merge = new MergeResult { MergeCommitted = true } }), Is.EqualTo("merged and committed"));
            Assert.That(OpSummary.Describe(new OpResult { Merge = new MergeResult() }), Is.EqualTo("merged"));
        }

        [Test]
        public void DescribesShelfOutcomes()
        {
            var shelf = new Shelf { Name = "wip", Entries = new List<ShelfEntry> { new ShelfEntry(), new ShelfEntry() } };
            Assert.That(OpSummary.Describe(new OpResult { Shelve = new ShelveResult { Shelf = shelf } }), Is.EqualTo("shelved 2 file(s) as \"wip\""));
            Assert.That(OpSummary.Describe(new OpResult { Shelve = new ShelveResult { Shelf = new Shelf { Name = "x" } } }), Does.StartWith("shelved 0"));
            Assert.That(OpSummary.Describe(new OpResult { ShelveApply = new ShelveApplyResult { Conflicts = new List<string> { "a" } } }), Does.StartWith("1 conflict"));
            Assert.That(OpSummary.Describe(new OpResult { ShelveApply = new ShelveApplyResult { Aborted = true } }), Is.EqualTo("shelf apply aborted"));
            Assert.That(OpSummary.Describe(new OpResult { ShelveApply = new ShelveApplyResult { Dropped = true } }), Is.EqualTo("shelf applied and deleted"));
            Assert.That(OpSummary.Describe(new OpResult { ShelveApply = new ShelveApplyResult() }), Is.EqualTo("shelf applied"));
        }

        [Test]
        public void HasConflicts()
        {
            Assert.That(OpSummary.HasConflicts(null), Is.False);
            Assert.That(OpSummary.HasConflicts(new OpResult { Merge = new MergeResult() }), Is.False);
            Assert.That(OpSummary.HasConflicts(new OpResult { Merge = new MergeResult { Conflicts = new List<string> { "a" } } }), Is.True);
            Assert.That(OpSummary.HasConflicts(new OpResult { ShelveApply = new ShelveApplyResult { Conflicts = new List<string> { "a" } } }), Is.True);
        }

        [Test]
        public void DescribesTeamEvents()
        {
            Assert.That(OpSummary.Describe((TeamEvent)null), Is.Null);
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "maintenance" }), Is.Null);
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "pushed", Actor = "Bob", Count = 2, Branch = "main", Message = "m" }), Is.EqualTo("Bob pushed 2 commit(s) to main: m"));
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "review_requested", Actor = "Bob", Number = 3, Message = "t" }), Does.Contain("!3"));
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "lock_released", Actor = "Bob", Path = "a.png" }), Is.EqualTo("a.png is no longer locked by Bob"));
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "lock_requested", Actor = "Bob", Path = "a.png" }), Is.EqualTo("Bob asks you to release a.png"));
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "lock_requested", Actor = "Bob", Path = "a.png", Message = "please" }), Is.EqualTo("Bob asks you to release a.png: please"));
        }

        [Test]
        public void ShelfName()
        {
            Assert.That(OpSummary.ShelfName("update", new DateTime(2026, 11, 17, 9, 5, 0)), Is.EqualTo("Before update 2026-11-17 09:05"));
        }
    }

    [TestFixture]
    public class ShelfBranchSessionTests
    {
        FakeDaemon fake;
        NipaSession session;

        [SetUp]
        public void SetUp()
        {
            fake = new FakeDaemon();
            var root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "nipa-shelf-clone"));
            session = new NipaSession(new DaemonClient(fake.Endpoint()), new ProjectPaths(Path.Combine(root, "game"), root))
            {
                ReconnectDelay = TimeSpan.FromMilliseconds(20),
            };
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{\"branch\":\"main\",\"modified\":[\"game/Assets/a.prefab\",\"game/Assets/a.prefab.meta\"],\"untracked\":[\"game/Assets/n.png\"]}");
            fake.On("LockStatus", "{\"user_id\":\"me\",\"branch\":\"main\",\"default_branch\":true,\"locks\":["
                + "{\"id\":\"L1\",\"path\":\"game/Assets/b.prefab\",\"held_by\":\"bob\",\"held_by_name\":\"Bob\",\"global\":true},"
                + "{\"id\":\"L2\",\"path\":\"game/Assets/m.png\",\"held_by\":\"me\",\"global\":true,\"requests\":[{\"id\":\"R1\",\"requester_id\":\"bob\",\"requester_name\":\"Bob\",\"message\":\"pls\"}]}]}");
            fake.On("WatchChanges", new Reply { Lines = new string[0], Hold = true });
            fake.On("WatchEvents", new Reply { Lines = new string[0], Hold = true });
        }

        [TearDown]
        public void TearDown()
        {
            session.Dispose();
            fake.Dispose();
        }

        string Body(string method) => fake.Requests.Last(r => r.Method == method).Body;

        [Test]
        public async Task MergeSendsSourceBranchAndReturnsResult()
        {
            fake.On("Merge", new Reply { Lines = new[] { "{\"started\":{}}", "{\"result\":{\"merge\":{\"conflicts\":[\"game/Assets/a.prefab\"]}}}" } });
            var events = 0;
            var result = await session.MergeAsync("feat", _ => events++);
            Assert.That(Body("Merge"), Does.Contain("\"source_branch\":\"feat\""));
            Assert.That(Body("Merge"), Does.Contain("\"abort\":false"));
            Assert.That(result.Merge.Conflicts, Is.EqualTo(new[] { "game/Assets/a.prefab" }));
            Assert.That(events, Is.EqualTo(2));
            Assert.That(fake.Requests.Count(r => r.Method == "Status"), Is.EqualTo(1));
        }

        [Test]
        public async Task CreateBranchForksFromCurrentBranch()
        {
            fake.On("ProxyBranchCreate", "{\"response\":{\"branch\":{\"name\":\"feat\"}}}");
            await session.RefreshAsync();
            var b = await session.CreateBranchAsync("feat");
            Assert.That(b.Name, Is.EqualTo("feat"));
            Assert.That(Body("ProxyBranchCreate"), Does.Contain("\"name\":\"feat\"").And.Contain("\"from_branch\":\"main\""));
        }

        [Test]
        public async Task CreateBranchOnDetachedTagForksFromTagCommit()
        {
            fake.On("Status", "{\"branch\":\"main\",\"head\":{\"kind\":\"tag\",\"name\":\"v1\"}}");
            fake.On("ProxyTagList", "{\"response\":{\"tags\":[{\"name\":\"v1\",\"commit_id\":\"c1\"}]}}");
            fake.On("ProxyBranchCreate", "{}");
            await session.RefreshAsync();
            Assert.That(await session.CreateBranchAsync("hotfix"), Is.Null);
            Assert.That(Body("ProxyBranchCreate"), Does.Contain("\"from_commit_id\":\"c1\""));
            fake.On("ProxyTagList", "{}");
            var ex = Assert.ThrowsAsync<DaemonException>(() => session.CreateBranchAsync("x"));
            Assert.That(ex.Code, Is.EqualTo(404));
        }

        [Test]
        public async Task DeleteBranchSendsName()
        {
            fake.On("ProxyBranchDelete", "{}");
            await session.DeleteBranchAsync("old");
            Assert.That(Body("ProxyBranchDelete"), Does.Contain("\"request\":{\"name\":\"old\"}"));
            fake.On("ProxyBranchDelete", new Reply { Status = 409, Body = "{\"code\":\"FailedPrecondition\",\"message\":\"protected\"}" });
            Assert.ThrowsAsync<DaemonException>(() => session.DeleteBranchAsync("main"));
        }

        [Test]
        public async Task SyncPlanReturnsOverwritten()
        {
            fake.On("SyncPlan", "{\"overwritten\":[\"game/Assets/a.prefab\"]}");
            Assert.That(await session.SyncPlanAsync("feat", null), Is.EqualTo(new[] { "game/Assets/a.prefab" }));
            Assert.That(Body("SyncPlan"), Does.Contain("\"branch\":\"feat\""));
            fake.On("SyncPlan", "{}");
            Assert.That(await session.SyncPlanAsync(null, "v1"), Is.Empty);
            Assert.That(Body("SyncPlan"), Does.Contain("\"tag\":\"v1\""));
        }

        [Test]
        public async Task ShelveExpandsMetaPairs()
        {
            await session.RefreshAsync();
            fake.On("ShelveCreate", new Reply { Lines = new[] { "{\"result\":{\"shelve\":{\"shelf\":{\"id\":\"s1\",\"name\":\"wip\",\"entries\":[{\"path\":\"game/Assets/a.prefab\"}]}}}}" } });
            var result = await session.ShelveAsync("wip", new[] { "game/Assets/a.prefab", "game/Assets/n.png" }, true, null);
            Assert.That(result.Shelve.Shelf.Id, Is.EqualTo("s1"));
            var body = Body("ShelveCreate");
            Assert.That(body, Does.Contain("\"paths\":[\"game/Assets/a.prefab\",\"game/Assets/a.prefab.meta\",\"game/Assets/n.png\"]"));
            Assert.That(body, Does.Contain("\"include_untracked\":true").And.Contain("\"name\":\"wip\""));
            await session.ShelveAsync("all", null, false, null);
            Assert.That(Body("ShelveCreate"), Does.Contain("\"paths\":[]"));
        }

        [Test]
        public async Task ListApplyAbortAndDropShelves()
        {
            fake.On("ShelveList", "{\"shelves\":[{\"id\":\"s1\",\"name\":\"wip\",\"created_at\":1700000000,\"branch\":\"main\",\"entries\":[{\"path\":\"a\",\"status\":\"modified\",\"untracked\":true}]}],\"applying\":\"s1\"}");
            var list = await session.ShelvesAsync();
            Assert.That(list.Applying, Is.EqualTo("s1"));
            Assert.That(list.Shelves.Single().CreatedAt, Is.EqualTo(1700000000));
            Assert.That(list.Shelves.Single().Entries.Single().Untracked, Is.True);
            fake.On("ShelveList", "{}");
            Assert.That((await session.ShelvesAsync()).Shelves, Is.Empty);

            fake.On("ShelveApply", new Reply { Lines = new[] { "{\"result\":{\"shelve_apply\":{\"shelf_id\":\"s1\",\"dropped\":false}}}" } });
            var applied = await session.ApplyShelfAsync("s1", true, null);
            Assert.That(applied.ShelveApply.ShelfId, Is.EqualTo("s1"));
            Assert.That(Body("ShelveApply"), Does.Contain("\"id\":\"s1\"").And.Contain("\"keep\":true").And.Contain("\"abort\":false"));
            await session.AbortShelfApplyAsync(null);
            Assert.That(Body("ShelveApply"), Does.Contain("\"abort\":true"));

            fake.On("ShelveDrop", "{}");
            await session.DropShelfAsync("s1");
            Assert.That(Body("ShelveDrop"), Does.Contain("\"id\":\"s1\""));
        }

        [Test]
        public async Task RequestReleaseUsesCoveringLockId()
        {
            await session.StartAsync();
            var mine = session.Locks.Lookup("game/Assets/m.png");
            Assert.That(mine.Lock.Requests.Single().RequesterName, Is.EqualTo("Bob"));
            Assert.That(session.Locks.Lookup("game/Assets/b.prefab.meta").Lock.Id, Is.EqualTo("L1"));

            fake.On("ProxyLockRequestRelease", "{}");
            await session.RequestReleaseAsync("game/Assets/b.prefab.meta", "  need it  ");
            Assert.That(Body("ProxyLockRequestRelease"), Does.Contain("\"lock_id\":\"L1\"").And.Contain("\"message\":\"need it\""));
            await session.RequestReleaseAsync("game/Assets/b.prefab", null);
            Assert.That(Body("ProxyLockRequestRelease"), Does.Contain("\"message\":\"\""));

            var ex = Assert.ThrowsAsync<DaemonException>(() => session.RequestReleaseAsync("game/Assets/m.png", "x"));
            Assert.That(ex.Code, Is.EqualTo(400));
            Assert.ThrowsAsync<DaemonException>(() => session.RequestReleaseAsync("game/Assets/free.png", "x"));
        }

        [Test]
        public async Task LockRequestedEventRefreshesLocks()
        {
            fake.On("WatchEvents", new Reply
            {
                Lines = new[] { "{\"events\":[{\"kind\":\"lock_requested\",\"actor\":\"Bob\",\"path\":\"game/Assets/m.png\",\"message\":\"pls\"}]}" },
                Hold = true,
            });
            var seen = new List<RepoActivity>();
            session.Activity += a => { lock (seen) seen.Add(a); };
            await session.StartAsync();
            Assert.That(SpinWait.SpinUntil(() => { lock (seen) return seen.Count == 1; }, 3000), Is.True);
            Assert.That(fake.Requests.Count(r => r.Method == "LockStatus"), Is.GreaterThanOrEqualTo(2));
            lock (seen)
                Assert.That(seen[0].Events.Single().Message, Is.EqualTo("pls"));
        }
    }
}
