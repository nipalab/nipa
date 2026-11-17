using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Session;
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    [TestFixture]
    public class MaintenanceNotesTests
    {
        Func<DateTimeOffset, string> saved;

        [SetUp]
        public void SetUp()
        {
            saved = MaintenanceNotes.Format;
            MaintenanceNotes.Format = t => t.UtcDateTime.ToString("HH:mm");
        }

        [TearDown]
        public void TearDown()
        {
            MaintenanceNotes.Format = saved;
        }

        static readonly DateTimeOffset Now = new DateTimeOffset(2026, 1, 1, 10, 0, 0, TimeSpan.Zero);

        [Test]
        public void CurrentPicksStrictestActiveWindow()
        {
            Assert.That(MaintenanceNotes.Current(null, Now), Is.Null);
            Assert.That(MaintenanceNotes.Current(new MaintenanceState(), Now), Is.Null);
            var state = new MaintenanceState
            {
                Server = new MaintenanceWindow { Mode = "read_only", Active = true },
                Project = new MaintenanceWindow { Mode = "full", Active = true, Message = " db move ", EndsAt = Now.AddHours(2), Project = "a/b" },
            };
            var n = MaintenanceNotes.Current(state, Now);
            Assert.That(n.Scope, Is.EqualTo("project"));
            Assert.That(n.Active, Is.True);
            Assert.That(n.BlocksWrites, Is.True);
            Assert.That(n.BlocksSync, Is.True);
            Assert.That(n.Text, Is.EqualTo("Project maintenance (closed). db move Estimated end 12:00. The server refuses every request."));

            state.Project = null;
            n = MaintenanceNotes.Current(state, Now);
            Assert.That(n.Scope, Is.EqualTo("server"));
            Assert.That(n.BlocksSync, Is.False);
            Assert.That(n.Text, Does.Contain("read-only").And.Contain("Update still works"));
        }

        [Test]
        public void CurrentReportsTheNextUpcomingWindow()
        {
            var state = new MaintenanceState
            {
                Server = new MaintenanceWindow { Mode = "frozen", StartsAt = Now.AddHours(5) },
                Org = new MaintenanceWindow { Mode = "read_only", StartsAt = Now.AddHours(1) },
                Project = new MaintenanceWindow { Mode = "full", StartsAt = Now.AddHours(-1) },
            };
            var n = MaintenanceNotes.Current(state, Now);
            Assert.That(n.Scope, Is.EqualTo("org"));
            Assert.That(n.Active, Is.False);
            Assert.That(n.BlocksWrites, Is.False);
            Assert.That(n.Text, Does.StartWith("Organization maintenance (read-only) starts 11:00."));
            state.Org = null;
            state.Server.Mode = "frozen";
            Assert.That(MaintenanceNotes.Current(state, Now).Text, Does.Contain("sign-in"));
            state.Server.StartsAt = null;
            Assert.That(MaintenanceNotes.Current(state, Now), Is.Null);
        }

        [Test]
        public void DescribesEvents()
        {
            Assert.That(MaintenanceNotes.Describe((TeamEvent)null), Is.Null);
            Assert.That(MaintenanceNotes.Describe(new TeamEvent { Kind = "maintenance" }), Is.Null);
            var w = new MaintenanceWindow { Mode = "full", StartsAt = Now, EndsAt = Now.AddHours(1), Message = "upgrade" };
            Assert.That(OpSummary.Describe(new TeamEvent { Kind = "maintenance", Message = "scheduled", Maintenance = w }),
                Is.EqualTo("Server maintenance (closed) scheduled for 10:00, estimated end 11:00: upgrade"));
            w.Org = "acme";
            Assert.That(MaintenanceNotes.Describe(new TeamEvent { Message = "started", Maintenance = w }), Does.StartWith("Organization maintenance (closed) started"));
            w.Project = "acme/game";
            w.Message = null;
            Assert.That(MaintenanceNotes.Describe(new TeamEvent { Message = "updated", Maintenance = w }), Is.EqualTo("Project maintenance (closed) updated, estimated end 11:00"));
            Assert.That(MaintenanceNotes.Describe(new TeamEvent { Message = "ended", Maintenance = w }), Is.EqualTo("Project maintenance (closed) ended"));
            w.EndsAt = null;
            w.Mode = "other";
            Assert.That(MaintenanceNotes.Describe(new TeamEvent { Message = "x", Maintenance = w }), Is.EqualTo("Project maintenance (other)"));
            w.Scope = "server";
            Assert.That(MaintenanceNotes.ScopeOf(w), Is.EqualTo("server"));
            Assert.That(MaintenanceNotes.ScopeOf(null), Is.EqualTo("server"));
            Assert.That(MaintenanceNotes.ModeText(null), Is.Empty);
        }

        [Test]
        public void ErrorTextAddsTheEstimatedEnd()
        {
            Assert.That(MaintenanceNotes.ErrorText(null), Is.Null);
            Assert.That(MaintenanceNotes.ErrorText(new InvalidOperationException("x")), Is.EqualTo("x"));
            var e = new DaemonException(503, "project is in maintenance", new MaintenanceWindow { Mode = "full", EndsAt = Now });
            Assert.That(MaintenanceNotes.ErrorText(new AggregateException(e)), Is.EqualTo("project is in maintenance\nEstimated end 10:00."));
            Assert.That(MaintenanceNotes.ErrorText(new DaemonException(503, "m", new MaintenanceWindow())), Is.EqualTo("m"));
        }

        [Test]
        public void DefaultFormatIsLocalTime()
        {
            Assert.That(saved(Now), Is.EqualTo(Now.ToLocalTime().ToString("yyyy-MM-dd HH:mm")));
        }
    }

    [TestFixture]
    public class PushChecksTests
    {
        [Test]
        public void ListsViolationsReferencesAndWarnings()
        {
            Assert.That(PushChecks.Warnings(null), Is.Empty);
            Assert.That(PushChecks.Warnings(new PushPlanResponse()), Is.Empty);
            var plan = new PushPlanResponse
            {
                AssetViolations = new List<AssetViolation>
                {
                    new AssetViolation { Path = "a.png", Message = "texture 4096 > 2048" },
                    new AssetViolation { Path = "b.png", Limit = "max_bytes", Actual = "3 MB", Max = "1 MB" },
                    new AssetViolation { Path = "c.png" },
                },
                AssetImpact = new AssetImpact
                {
                    Broken = new List<BrokenReference>
                    {
                        new BrokenReference { From = "s.unity", Target = "m.mat", Reason = "guid_changed" },
                        new BrokenReference { From = "s.unity", Raw = "abc" },
                    },
                    NewlyUnused = Enumerable.Range(0, PushChecks.MaxUnused + 2).Select(i => "u" + i).ToList(),
                    Truncated = true,
                },
                Warnings = new List<string> { "budget file invalid", " " },
            };
            var w = PushChecks.Warnings(plan);
            Assert.That(w[0], Is.EqualTo("a.png: texture 4096 > 2048"));
            Assert.That(w[1], Is.EqualTo("b.png: max_bytes is 3 MB, max 1 MB"));
            Assert.That(w[2], Is.EqualTo("c.png: budget"));
            Assert.That(w[3], Is.EqualTo("s.unity: broken reference to m.mat (guid changed)"));
            Assert.That(w[4], Is.EqualTo("s.unity: broken reference to abc"));
            Assert.That(w[5], Is.EqualTo("u0: no longer used by anything"));
            Assert.That(w, Does.Contain("2 more file(s) no longer used by anything"));
            Assert.That(w, Does.Contain("The reference scan stopped early; some references were not checked."));
            Assert.That(w.Last(), Is.EqualTo("budget file invalid"));
        }
    }

    [TestFixture]
    public class LockExpiryTests
    {
        static readonly DateTimeOffset Now = new DateTimeOffset(2026, 1, 1, 10, 0, 0, TimeSpan.Zero);

        [Test]
        public void DescribesTimeLeft()
        {
            Assert.That(LockExpiry.Describe(null, Now), Is.Null);
            Assert.That(LockExpiry.Describe(new FileLockDetail(), Now), Is.Null);
            Assert.That(LockExpiry.Describe(new FileLockDetail { ExpiresAt = Now }, Now), Is.EqualTo("expired"));
            Assert.That(LockExpiry.Describe(new FileLockDetail { ExpiresAt = Now.AddSeconds(10) }, Now), Is.EqualTo("expires in 1m"));
            Assert.That(LockExpiry.Describe(new FileLockDetail { ExpiresAt = Now.AddMinutes(90) }, Now), Is.EqualTo("expires in 1h 30m"));
            Assert.That(LockExpiry.Span(TimeSpan.FromHours(2)), Is.EqualTo("2h"));
            Assert.That(LockExpiry.Span(TimeSpan.FromHours(28)), Is.EqualTo("1d 4h"));
            Assert.That(LockExpiry.Span(TimeSpan.FromDays(2)), Is.EqualTo("2d"));
        }

        [Test]
        public void TtlIsClamped()
        {
            Assert.That(LockExpiry.Ttl(0), Is.Null);
            Assert.That(LockExpiry.Ttl(2), Is.EqualTo(TimeSpan.FromHours(2)));
            Assert.That(LockExpiry.Ttl(1000), Is.EqualTo(LockExpiry.MaxTtl));
            Assert.That(LockExpiry.RenewTtl(null), Is.EqualTo(TimeSpan.FromHours(1)));
            Assert.That(LockExpiry.RenewTtl(TimeSpan.FromSeconds(1)), Is.EqualTo(LockExpiry.MinTtl));
        }

        [Test]
        public void SettingsClampTtl()
        {
            var path = Path.Combine(Path.GetTempPath(), "nipa-ttl-" + Guid.NewGuid().ToString("N") + ".json");
            try
            {
                new NipaSettings { LockTtlHours = 500 }.Save(path);
                Assert.That(NipaSettings.Load(path).LockTtlHours, Is.EqualTo(168));
                new NipaSettings { LockTtlHours = -3 }.Save(path);
                Assert.That(NipaSettings.Load(path).LockTtlHours, Is.EqualTo(0));
            }
            finally
            {
                File.Delete(path);
            }
        }

        [Test]
        public void ParsesExpiryFromJson()
        {
            var l = JsonConvert.DeserializeObject<FileLockDetail>(
                "{\"id\":\"L\",\"acquired_at\":\"2026-01-01T09:00:00Z\",\"expires_at\":\"2026-01-01T11:00:00Z\"}", DaemonClient.JsonSettings);
            Assert.That(l.ExpiresAt, Is.EqualTo(Now.AddHours(1)));
            Assert.That(l.AcquiredAt, Is.EqualTo(Now.AddHours(-1)));
            Assert.That(JsonConvert.SerializeObject(new LockFileRequest { Path = "a" }, DaemonClient.JsonSettings), Does.Not.Contain("ttl"));
        }
    }

    [TestFixture]
    public class MaintenanceSessionTests
    {
        FakeDaemon fake;
        NipaSession session;

        [SetUp]
        public void SetUp()
        {
            fake = new FakeDaemon();
            var root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "nipa-maint-clone"));
            session = new NipaSession(new DaemonClient(fake.Endpoint()), new ProjectPaths(Path.Combine(root, "game"), root))
            {
                ReconnectDelay = TimeSpan.FromMilliseconds(20),
            };
            fake.On("WatchRepo", "{}");
            fake.On("Status", "{\"branch\":\"main\"}");
            fake.On("LockStatus", "{\"user_id\":\"me\",\"branch\":\"main\",\"default_branch\":true,\"locks\":["
                + "{\"id\":\"L2\",\"path\":\"game/Assets/m.png\",\"held_by\":\"me\",\"global\":true,\"expires_at\":\"2026-01-01T11:00:00Z\"},"
                + "{\"id\":\"L3\",\"path\":\"game/Assets/f.png\",\"branch\":\"feat\",\"held_by\":\"me\"}]}");
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
        public async Task ActivityStoresMaintenance()
        {
            var w = "{\"mode\":\"full\",\"active\":true,\"ends_at\":\"2026-01-01T11:00:00Z\"}";
            fake.On("WatchEvents", new Reply
            {
                Lines = new[]
                {
                    "{\"maintenance\":{\"server\":" + w + "}}",
                    "{\"incoming\":1,\"maintenance\":{\"server\":" + w + "}}",
                },
                Hold = true,
            });
            var got = new TaskCompletionSource<bool>();
            var count = 0;
            session.Activity += _ =>
            {
                if (Interlocked.Increment(ref count) == 2)
                    got.TrySetResult(true);
            };
            await session.StartAsync();
            Assert.That(await Task.WhenAny(got.Task, Task.Delay(5000)), Is.SameAs(got.Task));
            Assert.That(session.Maintenance.Server.Mode, Is.EqualTo("full"));
            var notice = session.MaintenanceNotice(DateTimeOffset.UtcNow);
            Assert.That(notice.BlocksSync, Is.True);
        }

        [Test]
        public async Task ActivityWithoutMaintenanceClearsEndedWindow()
        {
            var w = "{\"mode\":\"full\",\"active\":true}";
            fake.On("WatchEvents", new Reply
            {
                Lines = new[]
                {
                    "{\"maintenance\":{\"server\":" + w + "}}",
                    "{\"events\":[{\"kind\":\"maintenance\",\"message\":\"ended\"}]}",
                },
                Hold = true,
            });
            var got = new TaskCompletionSource<bool>();
            var count = 0;
            session.Activity += _ =>
            {
                if (Interlocked.Increment(ref count) == 2)
                    got.TrySetResult(true);
            };
            await session.StartAsync();
            Assert.That(await Task.WhenAny(got.Task, Task.Delay(5000)), Is.SameAs(got.Task));
            Assert.That(session.Maintenance, Is.Null);
            Assert.That(session.MaintenanceNotice(DateTimeOffset.UtcNow), Is.Null);
        }

        [Test]
        public async Task OpFailureCarriesMaintenance()
        {
            fake.On("Push", new Reply { Lines = new[] { "{\"failure\":{\"code\":503,\"message\":\"closed\",\"maintenance\":{\"scope\":\"project\",\"mode\":\"full\"}}}" } });
            var e = Assert.ThrowsAsync<DaemonException>(() => session.PushAsync("m", null));
            Assert.That(e.Code, Is.EqualTo(503));
            Assert.That(e.Maintenance.Scope, Is.EqualTo("project"));
            await Task.CompletedTask;
        }

        [Test]
        public void UnaryAndStreamErrorsCarryMaintenance()
        {
            fake.On("PushPlan", new Reply { Status = 503, Body = "{\"code\":\"Unavailable\",\"message\":\"closed\",\"maintenance\":{\"mode\":\"read_only\"}}" });
            var e = Assert.ThrowsAsync<DaemonException>(() => session.PushPlanAsync());
            Assert.That(e.Maintenance.Mode, Is.EqualTo("read_only"));
            fake.On("Update", new Reply { Lines = new[] { "{\"error\":{\"code\":\"Unavailable\",\"message\":\"closed\",\"maintenance\":{\"mode\":\"frozen\"}}}" } });
            e = Assert.ThrowsAsync<DaemonException>(() => session.UpdateAsync(null));
            Assert.That(e.Code, Is.EqualTo(503));
            Assert.That(e.Maintenance.Mode, Is.EqualTo("frozen"));
        }

        [Test]
        public async Task PushPlanParsesTheReport()
        {
            fake.On("PushPlan", "{\"changes\":[{\"path\":\"a\",\"status\":\"modified\",\"binary\":true,\"size_bytes\":\"12\"}],\"upload_objects\":2,\"upload_bytes\":\"40\","
                + "\"asset_violations\":[{\"path\":\"a\",\"message\":\"too big\"}],\"asset_impact\":{\"broken\":[{\"from\":\"s\",\"target\":\"t\"}],\"newly_unused\":[\"u\"],\"scanned\":3}}");
            var plan = await session.PushPlanAsync();
            Assert.That(Body("PushPlan"), Does.Contain("\"root\""));
            Assert.That(plan.Changes[0].SizeBytes, Is.EqualTo(12));
            Assert.That(plan.UploadBytes, Is.EqualTo(40));
            Assert.That(PushChecks.Warnings(plan), Has.Count.EqualTo(3));
        }

        [Test]
        public async Task LockSendsTtlFromSettings()
        {
            fake.On("ProxyLockFile", "{}");
            await session.RefreshLocksAsync();
            Assert.That(session.LockTtl, Is.Null);
            await session.LockAsync(new[] { "game/Assets/n.prefab" });
            Assert.That(Body("ProxyLockFile"), Does.Not.Contain("ttl"));
            session.ApplySettings(new NipaSettings { LockTtlHours = 2 });
            await session.LockAsync(new[] { "game/Assets/o.prefab" });
            Assert.That(Body("ProxyLockFile"), Does.Contain("\"ttl_seconds\":7200"));
        }

        [Test]
        public async Task RenewRelocksWithTtl()
        {
            fake.On("ProxyLockFile", "{}");
            await session.RefreshLocksAsync();
            var mine = session.Locks.Locks.First(l => l.Id == "L2");
            Assert.That(mine.ExpiresAt, Is.Not.Null);
            await session.RenewAsync(mine);
            Assert.That(Body("ProxyLockFile"), Does.Contain("\"ttl_seconds\":3600").And.Contain("\"branch\":\"\""));
            var feat = session.Locks.Locks.First(l => l.Id == "L3");
            session.ApplySettings(new NipaSettings { LockTtlHours = 3 });
            await session.RenewAsync(feat);
            Assert.That(Body("ProxyLockFile"), Does.Contain("\"ttl_seconds\":10800").And.Contain("\"branch\":\"feat\""));
            fake.On("ProxyLockFile", new Reply { Status = 409, Body = "{\"message\":\"no\"}" });
            var before = fake.Requests.Count(r => r.Method == "LockStatus");
            Assert.ThrowsAsync<DaemonException>(() => session.RenewAsync(feat));
            Assert.That(fake.Requests.Count(r => r.Method == "LockStatus"), Is.EqualTo(before + 1));
        }

        [Test]
        public async Task HistoryPagesFollowTheParent()
        {
            fake.On("ProxyCommitLog", "{\"response\":{\"commits\":[{\"commit_id\":\"c3\",\"parent_1_id\":\"c2\"},{\"commit_id\":\"c2\",\"parent_1_id\":\"c1\"}]}}");
            await session.RefreshAsync();
            var page = await session.HistoryPageAsync("game/Assets/a.png", null, 2);
            Assert.That(page.Commits, Has.Count.EqualTo(2));
            Assert.That(page.Next, Is.EqualTo("c1"));
            Assert.That(Body("ProxyCommitLog"), Does.Not.Contain("start_commit_id"));
            page = await session.HistoryPageAsync("game/Assets/a.png", "c1", 3);
            Assert.That(page.Next, Is.Null);
            Assert.That(Body("ProxyCommitLog"), Does.Contain("\"start_commit_id\":\"c1\"").And.Contain("\"limit\":3"));
            fake.On("ProxyCommitLog", "{\"response\":{\"commits\":[{\"commit_id\":\"c1\"}]}}");
            Assert.That((await session.HistoryPageAsync(null, null, 1)).Next, Is.Null);
        }

        [Test]
        public async Task HistoryPageFromATagStartsAtTheTag()
        {
            fake.On("Status", "{\"branch\":\"main\",\"head\":{\"kind\":\"tag\",\"name\":\"v1\"}}");
            fake.On("ProxyTagList", "{\"response\":{\"tags\":[{\"name\":\"v1\",\"commit_id\":\"t1\"}]}}");
            fake.On("ProxyCommitLog", "{\"response\":{\"commits\":[]}}");
            await session.RefreshAsync();
            await session.HistoryPageAsync(null, null);
            Assert.That(Body("ProxyCommitLog"), Does.Contain("\"start_commit_id\":\"t1\""));
            await session.HistoryPageAsync(null, "c9");
            Assert.That(Body("ProxyCommitLog"), Does.Contain("\"start_commit_id\":\"c9\""));
        }

        [Test]
        public void SidesMapEachOperation()
        {
            var merge = new ConflictsResponse { Op = "merge", Ours = new ConflictRevision { Id = "o" }, Theirs = new ConflictRevision { Id = "t" } };
            var s = NipaSession.Sides(merge, "a.png");
            Assert.That(s.Ours.Source, Is.EqualTo("revision"));
            Assert.That(s.Ours.Revision, Is.EqualTo("o"));
            Assert.That(s.Theirs.Revision, Is.EqualTo("t"));
            merge.Ours = new ConflictRevision();
            Assert.That(NipaSession.Sides(merge, "a.png").Ours, Is.Null);
            var shelf = new ConflictsResponse { Op = "shelf", Theirs = new ConflictRevision { Id = "s1" } };
            s = NipaSession.Sides(shelf, "a.png");
            Assert.That(s.Ours.Source, Is.EqualTo("snapshot"));
            Assert.That(s.Theirs.Source, Is.EqualTo("shelf"));
            Assert.That(s.Theirs.Revision, Is.EqualTo("s1"));
            Assert.That(NipaSession.Sides(null, "a").Ours, Is.Null);
        }

        [Test]
        public async Task PreviewAssemblesBothSides()
        {
            var a = Convert.ToBase64String(new byte[] { 1, 2 });
            var b = Convert.ToBase64String(new byte[] { 3 });
            fake.On("Preview", new Reply
            {
                Lines = new[]
                {
                    "{\"image\":{\"side\":\"old\",\"width\":4,\"height\":4,\"format\":\"png\"}}",
                    "{\"data\":{\"side\":\"old\",\"png\":\"" + a + "\"}}",
                    "{\"data\":{\"side\":\"old\",\"png\":\"" + b + "\"}}",
                    "{\"image\":{\"side\":\"new\",\"width\":8,\"height\":8,\"hdr\":true}}",
                    "{\"data\":{\"side\":\"new\",\"png\":\"" + b + "\"}}",
                    "{\"diff\":{\"changed_pixels\":\"5\",\"total_pixels\":\"16\",\"changed_ratio\":0.3,\"size_changed\":true}}",
                },
            });
            var c = await session.PreviewAsync(new BlobRef { Source = "revision", Revision = "o", Path = "a.png" }, new BlobRef { Source = "working", Path = "a.png" }, 256);
            Assert.That(Body("Preview"), Does.Contain("\"max_size\":256").And.Contain("\"new\":{\"source\":\"working\""));
            Assert.That(c.Old.Png, Is.EqualTo(new byte[] { 1, 2, 3 }));
            Assert.That(c.Old.Info.Width, Is.EqualTo(4));
            Assert.That(c.New.Png, Is.EqualTo(new byte[] { 3 }));
            Assert.That(c.New.Info.Hdr, Is.True);
            Assert.That(c.Diff.ChangedPixels, Is.EqualTo(5));
            Assert.That(c.Diff.SizeChanged, Is.True);
        }

        [Test]
        public void PreviewFailureThrows()
        {
            fake.On("Preview", new Reply { Lines = new[] { "{\"failure\":{\"code\":415,\"message\":\"unsupported\"}}" } });
            var e = Assert.ThrowsAsync<DaemonException>(() => session.PreviewAsync(null, new BlobRef { Source = "working", Path = "a.bin" }));
            Assert.That(e.Code, Is.EqualTo(415));
        }
    }
}
