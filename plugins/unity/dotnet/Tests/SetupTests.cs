using System.IO;
using System.Linq;
using System.Collections.Generic;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Setup;
using NUnit.Framework;

namespace Nipa.Core.Tests
{
    public class SetupTests
    {
        string root;

        [SetUp]
        public void Up()
        {
            root = Path.Combine(Path.GetTempPath(), "nipa-setup-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
        }

        [TearDown]
        public void Down() => Directory.Delete(root, true);

        SetupInputs Inputs(bool text = true, bool meta = true, string prefix = "") =>
            new SetupInputs { ForceText = text, VisibleMeta = meta, CloneRoot = root, ProjectPrefix = prefix };

        [Test]
        public void VisibleMetaDetection()
        {
            Assert.That(SetupChecker.IsVisibleMeta("Hidden Meta Files"), Is.False);
            Assert.That(SetupChecker.IsVisibleMeta("Visible Meta Files"), Is.True);
            Assert.That(SetupChecker.IsVisibleMeta("Nipa"), Is.True);
            Assert.That(SetupChecker.IsVisibleMeta(null), Is.True);
        }

        [Test]
        public void ReportsEverythingWrong()
        {
            var issues = SetupChecker.Check(Inputs(false, false));
            Assert.That(issues.Select(i => i.Kind),
                Is.EquivalentTo(new[] { SetupKind.ForceText, SetupKind.VisibleMeta, SetupKind.NipaIgnore }));
            Assert.That(issues.All(i => i.AutoFix), Is.True);
        }

        [Test]
        public void WriteIgnoreCreatesTemplateAndClearsIssue()
        {
            SetupChecker.WriteIgnore(root, "");
            var lines = File.ReadAllLines(Path.Combine(root, ".nipaignore"));
            Assert.That(lines[0], Is.EqualTo("Library/"));
            Assert.That(lines, Does.Contain("*.csproj"));
            Assert.That(lines.Length, Is.EqualTo(12));
            Assert.That(SetupChecker.Check(Inputs()), Is.Empty);
        }

        [Test]
        public void ExistingIgnoreWithLibraryIsFine()
        {
            File.WriteAllText(Path.Combine(root, ".nipaignore"), "/Library\n");
            Assert.That(SetupChecker.Check(Inputs()), Is.Empty);
            File.WriteAllText(Path.Combine(root, ".nipaignore"), "Library/**\n");
            Assert.That(SetupChecker.Check(Inputs()), Is.Empty);
        }

        [Test]
        public void ExistingIgnoreWithoutLibraryGetsMissingLinesAppended()
        {
            var path = Path.Combine(root, ".nipaignore");
            File.WriteAllText(path, "*.tmp\nTemp/");
            Assert.That(SetupChecker.Check(Inputs()).Count, Is.EqualTo(1));
            SetupChecker.WriteIgnore(root, "");
            var lines = File.ReadAllLines(path);
            Assert.That(lines[0], Is.EqualTo("*.tmp"));
            Assert.That(lines.Count(l => l == "Temp/"), Is.EqualTo(1));
            Assert.That(lines, Does.Contain("Library/"));
            Assert.That(SetupChecker.Check(Inputs()), Is.Empty);
        }

        [Test]
        public void WriteIgnoreIsNoopWhenComplete()
        {
            SetupChecker.WriteIgnore(root, "");
            var path = Path.Combine(root, ".nipaignore");
            var before = File.ReadAllText(path);
            SetupChecker.WriteIgnore(root, "");
            Assert.That(File.ReadAllText(path), Is.EqualTo(before));
        }

        [Test]
        public void SubfolderProjectPrefixesDirectoryRules()
        {
            var t = SetupChecker.IgnoreTemplate("games/hero/");
            Assert.That(t[0], Is.EqualTo("games/hero/Library/"));
            Assert.That(t.First(x => x.EndsWith("csproj")), Is.EqualTo("*.csproj"));
            SetupChecker.WriteIgnore(root, "games/hero");
            Assert.That(SetupChecker.Check(Inputs(prefix: "games/hero")), Is.Empty);
            Assert.That(SetupChecker.Check(Inputs(prefix: "other")).Count, Is.EqualTo(1));
        }

        [Test]
        public void RootOnlyRulesDoNotCoverSubfolderProject()
        {
            var path = Path.Combine(root, ".nipaignore");
            foreach (var rule in new[] { "/Library", "Library/**", "Library/" + "**" })
            {
                File.WriteAllText(path, rule + "\n");
                Assert.That(SetupChecker.Check(Inputs(prefix: "games/hero")), Has.Count.EqualTo(1), rule);
            }
        }

        [Test]
        public void UnanchoredAndPrefixedRulesCoverSubfolderProject()
        {
            var path = Path.Combine(root, ".nipaignore");
            foreach (var rule in new[] { "Library", "Library/", "**/Library/", "games/hero/Library", "/games/hero/Library/", "games/hero/Library/**" })
            {
                File.WriteAllText(path, rule + "\n");
                Assert.That(SetupChecker.Check(Inputs(prefix: "games/hero")), Is.Empty, rule);
            }
        }

        [Test]
        public void MetaWarningsOfferStagingOnlyForUnstagedMeta()
        {
            SetupChecker.WriteIgnore(root, "");
            var inputs = Inputs();
            inputs.MetaWarnings = new List<MetaWarning>();
            Assert.That(SetupChecker.Check(inputs), Is.Empty);
            inputs.MetaWarnings = new List<MetaWarning>
            {
                new MetaWarning { Path = "Assets/b.png.meta", Kind = SetupChecker.MetaNotStaged, Message = "is not staged" },
                new MetaWarning { Path = "Assets/a.png.meta", Kind = SetupChecker.MetaNotStaged, Message = "is not staged" },
                new MetaWarning { Path = "Assets/a.png.meta", Kind = SetupChecker.MetaNotStaged, Message = "dup" },
                new MetaWarning { Path = "Assets/c", Kind = "missing-meta", Message = "folder has no .meta file" },
            };
            var issue = SetupChecker.Check(inputs).Single();
            Assert.That(issue.Kind, Is.EqualTo(SetupKind.MetaFiles));
            Assert.That(issue.AutoFix, Is.True);
            Assert.That(issue.FixLabel, Is.EqualTo("Stage 2 files"));
            Assert.That(issue.Paths, Is.EqualTo(new[] { "Assets/a.png.meta", "Assets/b.png.meta" }));
            Assert.That(issue.Message, Does.StartWith("4 .meta warning(s)").And.Contain("• Assets/c: folder has no .meta file"));

            inputs.MetaWarnings = inputs.MetaWarnings.Skip(1).ToList();
            Assert.That(SetupChecker.Check(inputs).Single().FixLabel, Is.EqualTo("Stage 1 file"));

            inputs.MetaWarnings = Enumerable.Range(0, 8).Select(i => new MetaWarning { Path = "Assets/m" + i, Kind = "orphan-meta", Message = "x" }).ToList();
            issue = SetupChecker.Check(inputs).Single();
            Assert.That(issue.AutoFix, Is.False);
            Assert.That(issue.Paths, Is.Empty);
            Assert.That(issue.Message, Does.Contain("… and 2 more").And.Contain("nothing to stage"));
            Assert.That(SetupChecker.StagePaths(null), Is.Empty);
        }

        [Test]
        public void VersionTextNamesDaemonPluginAndCli()
        {
            var inputs = Inputs();
            inputs.DaemonVersion = "1.4.0";
            inputs.PluginVersion = "0.1.0";
            inputs.Binary = "/usr/bin/nipa";
            Assert.That(SetupChecker.VersionText(inputs), Is.EqualTo("Daemon 1.4.0 · plugin 0.1.0\nCLI: /usr/bin/nipa"));
            var none = new SetupInputs { Connected = false };
            Assert.That(SetupChecker.VersionText(none), Does.StartWith("Daemon not connected · plugin unknown\nCLI: not found"));
            Assert.That(SetupChecker.VersionText(new SetupInputs()), Does.StartWith("Daemon unknown"));
            Assert.That(new SetupIssue(SetupKind.ForceText, "m", true).FixLabel, Is.EqualTo("Fix"));
        }
    }
}
