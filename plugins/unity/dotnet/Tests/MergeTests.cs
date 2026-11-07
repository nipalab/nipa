using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Merge;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Session;
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    sealed class FakeProcess : IMergeProcess
    {
        public Func<IReadOnlyList<string>, int> Run = _ => 0;
        public string Exe;
        public List<string> Args;

        public Task<int> RunAsync(string exe, IReadOnlyList<string> args, CancellationToken ct)
        {
            Exe = exe;
            Args = args.ToList();
            return Task.FromResult(Run(args));
        }
    }

    [TestFixture]
    public class MergeToolTests
    {
        string dir;

        [SetUp]
        public void SetUp() => dir = Path.Combine(Path.GetTempPath(), "nipa-merge-" + Guid.NewGuid().ToString("N"));

        [TearDown]
        public void TearDown()
        {
            if (Directory.Exists(dir))
                Directory.Delete(dir, true);
        }

        [TestCase("Assets/a.unity", true)]
        [TestCase("Assets/a.PREFAB", true)]
        [TestCase("a.asset", true)]
        [TestCase("a.controller", true)]
        [TestCase("a.mat", true)]
        [TestCase("a.anim", true)]
        [TestCase("a.png", false)]
        [TestCase("a.cs", false)]
        [TestCase("", false)]
        [TestCase(null, false)]
        public void YamlExtensions(string path, bool expected)
        {
            Assert.That(MergeTool.IsYamlMergeable(path), Is.EqualTo(expected));
        }

        [Test]
        public void BuildsArgsInUnityOrder()
        {
            Assert.That(MergeTool.BuildArgs("b", "t", "o", "r"), Is.EqualTo(new[] { "merge", "-p", "b", "t", "o", "r" }));
        }

        [Test]
        public void ToolPathPointsIntoToolsFolder()
        {
            var p = MergeTool.ToolPath("/Unity/Data");
            Assert.That(p, Does.Contain("Tools"));
            Assert.That(p, Does.Contain("UnityYAMLMerge"));
        }

        [Test]
        public void DetectsMarkers()
        {
            Assert.That(MergeTool.HasMarkers(null), Is.False);
            Assert.That(MergeTool.HasMarkers(Encoding.UTF8.GetBytes("a: 1\nb: 2\n")), Is.False);
            Assert.That(MergeTool.HasMarkers(Encoding.UTF8.GetBytes("a\n<<<<<<< ours\nb\n")), Is.True);
            Assert.That(MergeTool.HasMarkers(Encoding.UTF8.GetBytes("a\n>>>>>>> theirs\n")), Is.True);
            Assert.That(MergeTool.HasMarkers(Encoding.UTF8.GetBytes("a\r\n=======\r\nb")), Is.True);
            Assert.That(MergeTool.HasMarkers(Encoding.UTF8.GetBytes("x: ======= \n")), Is.False);
        }

        [Test]
        public void ClassifiesConflicts()
        {
            Assert.That(MergeTool.Classify("text", false, true, "a.prefab"), Is.EqualTo(ConflictAction.Resolved));
            Assert.That(MergeTool.Classify("text", false, false, "a.prefab"), Is.EqualTo(ConflictAction.MergeTool));
            Assert.That(MergeTool.Classify("binary", true, false, "a.prefab"), Is.EqualTo(ConflictAction.Manual));
            Assert.That(MergeTool.Classify("modify_delete", false, false, "a.prefab"), Is.EqualTo(ConflictAction.Manual));
            Assert.That(MergeTool.Classify("text", false, false, "a.cs"), Is.EqualTo(ConflictAction.Manual));
        }

        [Test]
        public async Task RunReturnsMergedAndCleansUp()
        {
            var proc = new FakeProcess
            {
                Run = args => { File.WriteAllText(args.Last(), "merged: true\n"); return 0; },
            };
            var merged = await MergeTool.RunAsync(proc, "tool", dir, new byte[] { 1 }, new byte[] { 2 }, new byte[] { 3 }, default);
            Assert.That(Encoding.UTF8.GetString(merged), Is.EqualTo("merged: true\n"));
            Assert.That(proc.Exe, Is.EqualTo("tool"));
            Assert.That(proc.Args[0], Is.EqualTo("merge"));
            Assert.That(Directory.GetFiles(dir), Is.Empty);
        }

        [Test]
        public async Task RunHandlesMissingBaseAndFailureCodes()
        {
            var proc = new FakeProcess { Run = _ => 1 };
            Assert.That(await MergeTool.RunAsync(proc, "t", dir, null, new byte[] { 2 }, new byte[] { 3 }, default), Is.Null);
            proc.Run = _ => 0;
            Assert.That(await MergeTool.RunAsync(proc, "t", dir, null, null, null, default), Is.Null);
        }

        [Test]
        public async Task RunRejectsResultWithMarkers()
        {
            var proc = new FakeProcess
            {
                Run = args => { File.WriteAllText(args.Last(), "<<<<<<< a\nx\n=======\ny\n>>>>>>> b\n"); return 0; },
            };
            Assert.That(await MergeTool.RunAsync(proc, "t", dir, new byte[0], new byte[] { 1 }, new byte[] { 2 }, default), Is.Null);
        }

        [Test]
        public async Task SystemProcessRunsAndReportsExitCode()
        {
            if (Environment.OSVersion.Platform == PlatformID.Win32NT)
                Assert.Ignore("needs a POSIX shell");
            var p = new SystemMergeProcess();
            Assert.That(await p.RunAsync("/bin/sh", new[] { "-c", "exit 3" }, default), Is.EqualTo(3));
            Assert.That(await p.RunAsync("/bin/sh", new[] { "-c", "echo hi; echo err 1>&2" }, default), Is.EqualTo(0));
        }

        [Test]
        public void SystemProcessKillsOnCancel()
        {
            if (Environment.OSVersion.Platform == PlatformID.Win32NT)
                Assert.Ignore("needs a POSIX shell");
            using var cts = new CancellationTokenSource(150);
            Assert.CatchAsync<OperationCanceledException>(() =>
                new SystemMergeProcess().RunAsync("/bin/sh", new[] { "-c", "sleep 5" }, cts.Token));
        }
    }

    [TestFixture]
    public class ConflictSessionTests
    {
        FakeDaemon fake;
        NipaSession session;

        [SetUp]
        public void SetUp()
        {
            fake = new FakeDaemon();
            var root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "nipa-clone"));
            session = new NipaSession(new DaemonClient(fake.Endpoint()), new ProjectPaths(Path.Combine(root, "game"), root));
            fake.On("Status", "{\"branch\":\"main\"}");
        }

        [TearDown]
        public void TearDown()
        {
            session.Dispose();
            fake.Dispose();
        }

        static string Blob(string text) => "{\"data\":\"" + Convert.ToBase64String(Encoding.UTF8.GetBytes(text)) + "\"}";

        [Test]
        public async Task ListsBranchesTagsAndHistory()
        {
            fake.On("ProxyBranchList", "{\"response\":{\"branches\":[{\"name\":\"main\",\"is_default\":true},{\"name\":\"feat\"}]}}");
            fake.On("ProxyTagList", "{\"response\":{\"tags\":[{\"name\":\"v1\"}]}}");
            fake.On("ProxyCommitLog", "{\"response\":{\"commits\":[{\"commit_id\":\"x1\",\"message\":\"m\",\"author_name\":\"A\"}]}}");
            var branches = await session.BranchesAsync();
            Assert.That(branches.Select(b => b.Name), Is.EqualTo(new[] { "main", "feat" }));
            Assert.That(branches[0].IsDefault, Is.True);
            Assert.That((await session.TagsAsync()).Single().Name, Is.EqualTo("v1"));
            var log = await session.HistoryAsync("/Assets/a.prefab/");
            Assert.That(log.Single().AuthorName, Is.EqualTo("A"));
            var body = fake.Requests.Last().Body;
            Assert.That(body, Does.Contain("\"path\":\"Assets/a.prefab\""));
        }

        [Test]
        public async Task HistoryUsesCheckedOutBranch()
        {
            fake.On("Status", "{\"branch\":\"feat\"}");
            fake.On("ProxyCommitLog", "{}");
            await session.RefreshAsync();
            await session.HistoryAsync(null);
            Assert.That(fake.Requests.Last(r => r.Method == "ProxyCommitLog").Body, Does.Contain("\"branch\":\"feat\""));
        }

        [Test]
        public async Task HistoryOnDetachedTagStartsAtTagCommit()
        {
            fake.On("Status", "{\"branch\":\"main\",\"head\":{\"kind\":\"tag\",\"name\":\"v1\"}}");
            fake.On("ProxyTagList", "{\"response\":{\"tags\":[{\"name\":\"v0\",\"commit_id\":\"c0\"},{\"name\":\"v1\",\"commit_id\":\"c1\"}]}}");
            fake.On("ProxyCommitLog", "{}");
            await session.RefreshAsync();
            await session.HistoryAsync(null);
            Assert.That(fake.Requests.Last(r => r.Method == "ProxyCommitLog").Body, Does.Contain("\"start_commit_id\":\"c1\""));
            fake.On("ProxyTagList", "{}");
            var ex = Assert.ThrowsAsync<DaemonException>(() => session.HistoryAsync(null));
            Assert.That(ex.Code, Is.EqualTo(404));
        }

        [Test]
        public async Task FindTagPagesPastTheFirstPage()
        {
            string Page(int from, int count) => "{\"response\":{\"tags\":[" + string.Join(",",
                Enumerable.Range(from, count).Select(i => "{\"name\":\"t" + i + "\",\"id\":\"id" + i + "\",\"created_at\":\"ts" + i + "\",\"commit_id\":\"c" + i + "\"}")) + "]}}";
            fake.Handlers["ProxyTagList"] = r => new Reply
            {
                Body = r.Body.Contains("\"last_id\":\"id199\"") ? Page(200, 5) : Page(0, 200),
            };
            var tag = await session.FindTagAsync("t203");
            Assert.That(tag.CommitId, Is.EqualTo("c203"));
            Assert.That(fake.Requests.Last(r => r.Method == "ProxyTagList").Body, Does.Contain("\"last_created_at\":\"ts199\""));
            Assert.That(await session.FindTagAsync("missing"), Is.Null);
        }

        [Test]
        public async Task FindTagStopsAtPageLimit()
        {
            string Page() => "{\"response\":{\"tags\":[" + string.Join(",",
                Enumerable.Range(0, 200).Select(i => "{\"name\":\"t" + i + "\",\"id\":\"i\"}")) + "]}}";
            fake.On("ProxyTagList", Page());
            Assert.That(await session.FindTagAsync("nope"), Is.Null);
            Assert.That(fake.Requests.Count(r => r.Method == "ProxyTagList"), Is.EqualTo(50));
        }

        [Test]
        public async Task EmptyResponsesGiveEmptyLists()
        {
            fake.On("ProxyBranchList", "{}");
            fake.On("ProxyTagList", "{}");
            fake.On("ProxyCommitLog", "{}");
            Assert.That(await session.BranchesAsync(), Is.Empty);
            Assert.That(await session.TagsAsync(), Is.Empty);
            Assert.That(await session.HistoryAsync(null), Is.Empty);
        }

        [Test]
        public async Task ConflictsDefaultsFiles()
        {
            fake.On("Conflicts", "{\"op\":\"merge\",\"ours\":{\"id\":\"o\"}}");
            var set = await session.ConflictsAsync();
            Assert.That(set.Op, Is.EqualTo("merge"));
            Assert.That(set.Files, Is.Empty);
        }

        [Test]
        public async Task ResolveSendsBase64ContentAndAppliesStatus()
        {
            fake.On("ResolveConflict", "{\"staged\":[\"game/Assets/a.prefab\"],\"branch\":\"main\"}");
            await session.ResolveAsync("game/Assets/a.prefab", "content", new byte[] { 1, 2, 3 });
            var req = fake.Requests.Last();
            Assert.That(req.Body, Does.Contain("\"choice\":\"content\""));
            Assert.That(req.Body, Does.Contain("\"content\":\"AQID\""));
            Assert.That(session.Cache.Get("game/Assets/a.prefab").HasFlag(Nipa.Editor.Core.Status.FileState.Staged), Is.True);
        }

        [Test]
        public async Task ReadRevisionCollectsChunks()
        {
            fake.On("ReadBlob", new Reply { Lines = new[] { "{\"info\":{\"path\":\"a\"}}", Blob("ab"), Blob("cd") } });
            var data = await session.ReadRevisionAsync("r1", "a");
            Assert.That(Encoding.UTF8.GetString(data), Is.EqualTo("abcd"));
            Assert.That(fake.Requests.Last().Body, Does.Contain("\"source\":\"revision\""));
            Assert.That(await session.ReadRevisionAsync("", "a"), Is.Null);
        }

        [Test]
        public async Task ReadRevisionMissingFileIsNull()
        {
            fake.On("ReadBlob", new Reply { Lines = new[] { "{\"failure\":{\"code\":404,\"message\":\"gone\"}}" } });
            Assert.That(await session.ReadRevisionAsync("r1", "a"), Is.Null);
            fake.On("ReadBlob", new Reply { Status = 404, Body = "{\"message\":\"gone\"}" });
            Assert.That(await session.ReadRevisionAsync("r1", "a"), Is.Null);
        }

        [Test]
        public void ReadRevisionFailureThrows()
        {
            fake.On("ReadBlob", new Reply { Lines = new[] { "{\"failure\":{\"code\":500,\"message\":\"bad\"}}" } });
            var ex = Assert.ThrowsAsync<DaemonException>(() => session.ReadRevisionAsync("r1", "a"));
            Assert.That(ex.Message, Is.EqualTo("bad"));
        }

        [Test]
        public async Task MergeToolResolveSubmitsContent()
        {
            fake.Handlers["ReadBlob"] = r =>
            {
                var rev = r.Body.Contains("\"revision\":\"b\"") ? "base" : r.Body.Contains("\"revision\":\"t\"") ? "theirs" : "ours";
                return new Reply { Lines = new[] { Blob(rev) } };
            };
            fake.On("ResolveConflict", "{\"branch\":\"main\"}");
            var set = new ConflictsResponse
            {
                Base = new ConflictRevision { Id = "b" },
                Theirs = new ConflictRevision { Id = "t" },
                Ours = new ConflictRevision { Id = "o" },
            };
            var proc = new FakeProcess
            {
                Run = args =>
                {
                    Assert.That(File.ReadAllText(args[2]), Is.EqualTo("base"));
                    Assert.That(File.ReadAllText(args[3]), Is.EqualTo("theirs"));
                    Assert.That(File.ReadAllText(args[4]), Is.EqualTo("ours"));
                    File.WriteAllText(args[5], "merged");
                    return 0;
                },
            };
            var dir = Path.Combine(Path.GetTempPath(), "nipa-mt-" + Guid.NewGuid().ToString("N"));
            try
            {
                Assert.That(await session.MergeToolResolveAsync(set, "Assets/a.prefab", "tool", proc, dir), Is.True);
            }
            finally
            {
                if (Directory.Exists(dir))
                    Directory.Delete(dir, true);
            }
            var body = fake.Requests.Last().Body;
            Assert.That(body, Does.Contain("\"choice\":\"content\""));
            Assert.That(body, Does.Contain(Convert.ToBase64String(Encoding.UTF8.GetBytes("merged"))));
        }

        [Test]
        public async Task MergeToolResolveFallsBackWhenSideMissingOrToolFails()
        {
            fake.Handlers["ReadBlob"] = _ => new Reply { Lines = new[] { Blob("x") } };
            var set = new ConflictsResponse
            {
                Base = new ConflictRevision { Id = "b" },
                Theirs = new ConflictRevision { Id = "t" },
                Ours = new ConflictRevision { Id = "" },
            };
            var dir = Path.Combine(Path.GetTempPath(), "nipa-mt-" + Guid.NewGuid().ToString("N"));
            Assert.That(await session.MergeToolResolveAsync(set, "a.prefab", "tool", new FakeProcess(), dir), Is.False);
            set.Ours = new ConflictRevision { Id = "o" };
            try
            {
                Assert.That(await session.MergeToolResolveAsync(set, "a.prefab", "tool", new FakeProcess { Run = _ => 1 }, dir), Is.False);
            }
            finally
            {
                if (Directory.Exists(dir))
                    Directory.Delete(dir, true);
            }
            Assert.That(fake.Requests.Any(r => r.Method == "ResolveConflict"), Is.False);
        }

        [Test]
        public async Task MergeAbortAndRevertStepsSendFlags()
        {
            var done = "{\"result\":{\"sync\":{\"branch\":\"main\"}}}";
            fake.On("Merge", new Reply { Lines = new[] { done } });
            fake.On("Revert", new Reply { Lines = new[] { done } });
            await session.AbortMergeAsync(null);
            Assert.That(fake.Requests.Last(r => r.Method == "Merge").Body, Does.Contain("\"abort\":true"));
            foreach (var step in new[] { "abort", "continue", "skip" })
            {
                await session.RevertStepAsync(step, null);
                var body = fake.Requests.Last(r => r.Method == "Revert").Body;
                Assert.That(body, Does.Contain("\"" + step + "\":true"));
            }
        }
    }
}
