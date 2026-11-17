using System;
using System.Collections.Generic;
using System.Linq;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Session;
using Nipa.Editor.Core.Setup;
using UnityEditor;
using UnityEngine;

namespace Nipa.Editor
{
    internal static class NipaSetup
    {
        const string ProviderName = "Nipa";
        static string binarySetting;
        static string binary;
        static string pluginVersion;

        public static List<SetupIssue> Check(NipaSession session, List<MetaWarning> warnings)
        {
            return SetupChecker.Check(Inputs(session, warnings));
        }

        static SetupInputs Inputs(NipaSession session, List<MetaWarning> warnings)
        {
            return new SetupInputs
            {
                ForceText = EditorSettings.serializationMode == SerializationMode.ForceText,
                VisibleMeta = SetupChecker.IsVisibleMeta(EditorSettings.externalVersionControl),
                CloneRoot = session?.Root,
                ProjectPrefix = session?.Paths.Prefix ?? string.Empty,
                MetaWarnings = warnings,
                Connected = session != null,
                DaemonVersion = NipaHost.DaemonVersion,
                PluginVersion = PluginVersion(),
                Binary = Binary(),
            };
        }

        public static string VersionText(NipaSession session) => SetupChecker.VersionText(Inputs(session, null));

        static string PluginVersion()
        {
            if (pluginVersion == null)
                pluginVersion = UnityEditor.PackageManager.PackageInfo.FindForAssembly(typeof(NipaSetup).Assembly)?.version ?? string.Empty;
            return pluginVersion;
        }

        static string Binary()
        {
            var setting = NipaSettingsStore.BinaryPath;
            if (setting == binarySetting && binary != null)
                return binary;
            binarySetting = setting;
            try
            {
                binary = Connector.ResolveBinary(new ConnectorOptions { Binary = setting }, Environment.GetEnvironmentVariable);
            }
            catch (System.IO.FileNotFoundException)
            {
                binary = string.Empty;
            }
            return binary;
        }

        public static void Fix(NipaSession session, SetupIssue issue)
        {
            switch (issue.Kind)
            {
                case SetupKind.ForceText:
                    EditorSettings.serializationMode = SerializationMode.ForceText;
                    break;
                case SetupKind.VisibleMeta:
                    EditorSettings.externalVersionControl = ProviderName;
                    break;
                case SetupKind.NipaIgnore:
                    SetupChecker.WriteIgnore(session.Root, session.Paths.Prefix);
                    NipaHost.Refresh();
                    break;
                case SetupKind.MetaFiles:
                    StageMeta(issue.Paths.ToList());
                    break;
            }
        }

        public static void StageMeta(List<string> paths)
        {
            if (paths.Count > 0)
                NipaHost.Run("Stage .meta files", (s, _) => s.StageAsync(paths));
        }

        public static void Draw(NipaSession session, List<MetaWarning> warnings)
        {
            EditorGUILayout.HelpBox(VersionText(session), MessageType.None);
            var issues = Check(session, warnings);
            if (issues.Count == 0)
            {
                EditorGUILayout.HelpBox("Project setup looks good.", MessageType.Info);
                return;
            }
            foreach (var issue in issues)
            {
                EditorGUILayout.HelpBox(issue.Message, MessageType.Warning);
                using (new EditorGUI.DisabledScope(issue.Kind == SetupKind.MetaFiles && NipaHost.Busy != null))
                {
                    if (issue.AutoFix && GUILayout.Button(issue.FixLabel))
                    {
                        Fix(session, issue);
                        break;
                    }
                }
            }
        }
    }
}
