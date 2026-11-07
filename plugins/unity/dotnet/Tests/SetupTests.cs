using System.IO;
using System.Linq;
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
    }
}
