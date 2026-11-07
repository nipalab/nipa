using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace Nipa.Editor.Core.Merge
{
    public enum ConflictAction
    {
        Manual,
        MergeTool,
        Resolved,
    }

    public interface IMergeProcess
    {
        Task<int> RunAsync(string exe, IReadOnlyList<string> args, CancellationToken ct);
    }

    public sealed class SystemMergeProcess : IMergeProcess
    {
        public async Task<int> RunAsync(string exe, IReadOnlyList<string> args, CancellationToken ct)
        {
            var info = new ProcessStartInfo(exe)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
            };
            foreach (var a in args)
                info.ArgumentList.Add(a);
            using (var p = Process.Start(info))
            {
                var drain = Task.WhenAll(p.StandardOutput.ReadToEndAsync(), p.StandardError.ReadToEndAsync());
                using (ct.Register(() => TryKill(p)))
                {
                    await Task.Run(p.WaitForExit).ConfigureAwait(false);
                    await drain.ConfigureAwait(false);
                }
                ct.ThrowIfCancellationRequested();
                return p.ExitCode;
            }
        }

        static void TryKill(Process p)
        {
            try
            {
                p.Kill();
            }
            catch (InvalidOperationException)
            {
            }
        }
    }

    public static class MergeTool
    {
        static readonly HashSet<string> YamlExtensions = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            ".unity", ".prefab", ".asset", ".controller", ".mat", ".anim",
        };

        public static bool IsYamlMergeable(string path)
        {
            return !string.IsNullOrEmpty(path) && YamlExtensions.Contains(Path.GetExtension(path));
        }

        public static string ToolPath(string applicationContentsPath)
        {
            var exe = Environment.OSVersion.Platform == PlatformID.Win32NT ? "UnityYAMLMerge.exe" : "UnityYAMLMerge";
            return Path.Combine(applicationContentsPath, "Tools", exe);
        }

        public static List<string> BuildArgs(string basePath, string theirs, string ours, string output)
        {
            return new List<string> { "merge", "-p", basePath, theirs, ours, output };
        }

        public static bool HasMarkers(byte[] content)
        {
            if (content == null)
                return false;
            var text = Encoding.UTF8.GetString(content);
            foreach (var line in text.Split('\n'))
            {
                if (line.StartsWith("<<<<<<<", StringComparison.Ordinal)
                    || line.StartsWith(">>>>>>>", StringComparison.Ordinal)
                    || line.TrimEnd('\r') == "=======")
                    return true;
            }
            return false;
        }

        public static ConflictAction Classify(string kind, bool binary, bool resolved, string path)
        {
            if (resolved)
                return ConflictAction.Resolved;
            if (kind == "text" && !binary && IsYamlMergeable(path))
                return ConflictAction.MergeTool;
            return ConflictAction.Manual;
        }

        public static async Task<byte[]> RunAsync(IMergeProcess process, string exe, string workDir,
            byte[] baseData, byte[] theirs, byte[] ours, CancellationToken ct)
        {
            Directory.CreateDirectory(workDir);
            var b = Path.Combine(workDir, "base");
            var t = Path.Combine(workDir, "theirs");
            var o = Path.Combine(workDir, "ours");
            var r = Path.Combine(workDir, "merged");
            try
            {
                File.WriteAllBytes(b, baseData ?? new byte[0]);
                File.WriteAllBytes(t, theirs ?? new byte[0]);
                File.WriteAllBytes(o, ours ?? new byte[0]);
                var code = await process.RunAsync(exe, BuildArgs(b, t, o, r), ct).ConfigureAwait(false);
                if (code != 0 || !File.Exists(r))
                    return null;
                var merged = File.ReadAllBytes(r);
                return HasMarkers(merged) ? null : merged;
            }
            finally
            {
                foreach (var f in new[] { b, t, o, r })
                {
                    try
                    {
                        File.Delete(f);
                    }
                    catch (IOException)
                    {
                    }
                }
            }
        }
    }
}
