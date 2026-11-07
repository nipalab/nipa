using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;

namespace Nipa.Editor.Core.Setup
{
    public enum SetupKind
    {
        ForceText,
        VisibleMeta,
        NipaIgnore,
    }

    public sealed class SetupIssue
    {
        public SetupKind Kind { get; }
        public string Message { get; }
        public bool AutoFix { get; }

        public SetupIssue(SetupKind kind, string message, bool autoFix)
        {
            Kind = kind;
            Message = message;
            AutoFix = autoFix;
        }
    }

    public sealed class SetupInputs
    {
        public bool ForceText { get; set; }
        public bool VisibleMeta { get; set; }
        public string CloneRoot { get; set; }
        public string ProjectPrefix { get; set; } = string.Empty;
    }

    public static class SetupChecker
    {
        public const string IgnoreFile = ".nipaignore";
        public const string HiddenMetaMode = "Hidden Meta Files";

        static readonly string[] Template =
        {
            "Library/", "Temp/", "Logs/", "Obj/", "Build/", "Builds/", "UserSettings/",
            "MemoryCaptures/", "*.csproj", "*.sln", ".vs/", ".idea/",
        };

        public static bool IsVisibleMeta(string externalVersionControl)
        {
            return !string.Equals(externalVersionControl, HiddenMetaMode, StringComparison.OrdinalIgnoreCase);
        }

        public static IReadOnlyList<string> IgnoreTemplate(string projectPrefix)
        {
            var prefix = (projectPrefix ?? string.Empty).Trim('/');
            if (prefix.Length == 0)
                return Template;
            return Template.Select(t => t.StartsWith("*", StringComparison.Ordinal) ? t : prefix + "/" + t).ToList();
        }

        public static List<SetupIssue> Check(SetupInputs inputs)
        {
            var issues = new List<SetupIssue>();
            if (!inputs.ForceText)
                issues.Add(new SetupIssue(SetupKind.ForceText,
                    "Asset Serialization must be Force Text so scenes and prefabs can be diffed and merged.", true));
            if (!inputs.VisibleMeta)
                issues.Add(new SetupIssue(SetupKind.VisibleMeta,
                    "Meta files must be visible: Hidden Meta Files breaks asset GUIDs in version control.", true));

            var path = IgnorePath(inputs.CloneRoot);
            if (!File.Exists(path))
            {
                issues.Add(new SetupIssue(SetupKind.NipaIgnore,
                    ".nipaignore is missing; Library/, Temp/ and other generated folders would show up as changes.", true));
            }
            else if (!IgnoresLibrary(File.ReadAllLines(path), inputs.ProjectPrefix))
            {
                issues.Add(new SetupIssue(SetupKind.NipaIgnore,
                    ".nipaignore does not ignore the Unity Library folder.", true));
            }
            return issues;
        }

        public static void WriteIgnore(string cloneRoot, string projectPrefix)
        {
            var path = IgnorePath(cloneRoot);
            var template = IgnoreTemplate(projectPrefix);
            if (!File.Exists(path))
            {
                File.WriteAllText(path, string.Join("\n", template) + "\n", new UTF8Encoding(false));
                return;
            }
            var existing = File.ReadAllText(path);
            var have = new HashSet<string>(existing.Split('\n').Select(Norm));
            var missing = template.Where(t => !have.Contains(Norm(t))).ToList();
            if (missing.Count == 0)
                return;
            var sb = new StringBuilder(existing);
            if (existing.Length > 0 && !existing.EndsWith("\n", StringComparison.Ordinal))
                sb.Append('\n');
            foreach (var line in missing)
                sb.Append(line).Append('\n');
            File.WriteAllText(path, sb.ToString(), new UTF8Encoding(false));
        }

        static bool IgnoresLibrary(IEnumerable<string> lines, string projectPrefix)
        {
            var prefix = (projectPrefix ?? string.Empty).Trim('/');
            var basePath = prefix.Length == 0 ? "Library" : prefix + "/Library";
            var accepted = new HashSet<string> { "Library", "Library/", "**/Library", "**/Library/" };
            foreach (var form in new[] { basePath, basePath + "/", basePath + "/**" })
            {
                accepted.Add(form);
                accepted.Add("/" + form);
            }
            return lines.Select(l => l.Trim()).Any(accepted.Contains);
        }

        static string Norm(string line)
        {
            return line.Trim().TrimStart('/');
        }

        static string IgnorePath(string cloneRoot)
        {
            return Path.Combine(cloneRoot, IgnoreFile);
        }
    }
}
