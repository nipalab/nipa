using System.IO;
using Nipa.Editor.Core.Session;
using UnityEditor;

namespace Nipa.Editor
{
    internal static class NipaSettingsStore
    {
        const string BinaryKey = "Nipa.BinaryPath";
        const string ProjectFile = "ProjectSettings/NipaSettings.json";

        public static string BinaryPath
        {
            get => EditorUserSettings.GetConfigValue(BinaryKey) ?? string.Empty;
            set => EditorUserSettings.SetConfigValue(BinaryKey, value);
        }

        public static NipaSettings LoadProject()
        {
            return NipaSettings.Load(ProjectFile);
        }

        public static void SaveProject(NipaSettings settings)
        {
            settings.Save(ProjectFile);
        }

        public static string ProjectDir => Directory.GetParent(UnityEngine.Application.dataPath).FullName;
    }
}
