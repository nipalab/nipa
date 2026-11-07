using System.Collections.Generic;
using Nipa.Editor.Core.Session;
using Nipa.Editor.Core.Setup;
using UnityEditor;

namespace Nipa.Editor
{
    internal static class NipaSetup
    {
        const string ProviderName = "Nipa";

        public static List<SetupIssue> Check(NipaSession session)
        {
            return SetupChecker.Check(new SetupInputs
            {
                ForceText = EditorSettings.serializationMode == SerializationMode.ForceText,
                VisibleMeta = SetupChecker.IsVisibleMeta(EditorSettings.externalVersionControl),
                CloneRoot = session.Root,
                ProjectPrefix = session.Paths.Prefix,
            });
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
            }
        }

        public static void Draw(NipaSession session)
        {
            var issues = Check(session);
            if (issues.Count == 0)
            {
                EditorGUILayout.HelpBox("Project setup looks good.", MessageType.Info);
                return;
            }
            foreach (var issue in issues)
            {
                EditorGUILayout.HelpBox(issue.Message, MessageType.Warning);
                if (issue.AutoFix && UnityEngine.GUILayout.Button("Fix"))
                {
                    Fix(session, issue);
                    break;
                }
            }
        }
    }
}
