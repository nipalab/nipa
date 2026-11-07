using System.Linq;
using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Paths;
using Nipa.Editor.Core.Status;
using UnityEditor;
using UnityEditor.VersionControl;
using UnityEngine;

namespace Nipa.Editor
{
    [VersionControl("Nipa", "Nipa")]
    public class NipaVersionControl : VersionControlObject, IIconOverlayExtension, IPopupMenuExtension, ISettingsInspectorExtension
    {
        const float IconSize = 12f;

        public override void OnActivate()
        {
            NipaHost.Activate();
        }

        public override void OnDeactivate()
        {
            NipaHost.Deactivate();
        }

        public override bool isConnected => NipaHost.Session != null;

        public override void Refresh()
        {
            NipaHost.Refresh();
        }

        public void DrawOverlay(string assetPath, IconOverlayType type, Rect rect)
        {
            if (type != IconOverlayType.Project)
                return;
            var session = NipaHost.Session;
            if (session == null || string.IsNullOrEmpty(assetPath))
                return;
            var clone = session.Paths.ToClonePath(assetPath);
            var state = session.Cache.Get(clone) | session.Cache.Get(ProjectPaths.MetaOf(clone));
            var size = Mathf.Min(IconSize, rect.height * 0.5f);
            var icon = NipaIcons.For(state);
            if (icon != null)
                GUI.DrawTexture(new Rect(rect.x, rect.y, size, size), icon);
            var info = session.Locks.Lookup(clone);
            if (info.State != LockState.Free)
            {
                var tex = info.State == LockState.Mine ? NipaIcons.LockMine : NipaIcons.LockTheirs;
                GUI.DrawTexture(new Rect(rect.xMax - size, rect.y, size, size), tex);
            }
            if (assetPath == "Assets" && session.Incoming > 0)
                GUI.DrawTexture(new Rect(rect.xMax - size, rect.yMax - size, size, size), NipaIcons.Behind);
        }

        public void DisplayPopupMenu(Rect position)
        {
            EditorUtility.DisplayPopupMenu(position, "CONTEXT/Nipa", null);
        }

        public void OnInspectorGUI()
        {
            EditorGUILayout.LabelField("Nipa", EditorStyles.boldLabel);
            var session = NipaHost.Session;
            string status;
            if (session != null)
                status = "Connected to " + session.Repo?.Url + " (" + session.Cache.Branch + ")";
            else if (NipaHost.Connecting)
                status = "Connecting to the nipa daemon...";
            else
                status = NipaHost.Error ?? "Not connected";
            EditorGUILayout.HelpBox(status, session != null ? MessageType.Info : MessageType.Warning);

            EditorGUI.BeginChangeCheck();
            var binary = EditorGUILayout.TextField(
                new GUIContent("nipa binary", "Path to the nipa CLI. Empty uses NIPA_BIN, then PATH."),
                NipaSettingsStore.BinaryPath);
            if (EditorGUI.EndChangeCheck())
                NipaSettingsStore.BinaryPath = binary;

            var settings = NipaSettingsStore.LoadProject();
            EditorGUI.BeginChangeCheck();
            var poll = EditorGUILayout.IntField(
                new GUIContent("Poll interval (s)", "Safety-net status refresh when no file events arrive. Shared in ProjectSettings/NipaSettings.json; applies on reconnect."),
                settings.PollIntervalSeconds);
            if (EditorGUI.EndChangeCheck())
            {
                settings.PollIntervalSeconds = poll;
                NipaSettingsStore.SaveProject(settings);
            }

            EditorGUI.BeginChangeCheck();
            var strict = EditorGUILayout.Toggle(
                new GUIContent("Strict locking", "Files with a listed extension are read-only until locked by you."),
                settings.LockStrict);
            var extensions = EditorGUILayout.TextField(
                new GUIContent("Locked extensions", "Comma separated. Only used by strict locking."),
                string.Join(", ", settings.LockExtensions));
            if (EditorGUI.EndChangeCheck())
            {
                settings.LockStrict = strict;
                settings.LockExtensions = extensions.Split(',').Select(e => e.Trim()).Where(e => e.Length > 0).ToList();
                NipaSettingsStore.SaveProject(settings);
                NipaHost.ApplySettings();
            }

            if (GUILayout.Button("Reconnect"))
                NipaHost.Reconnect();
            if (GUILayout.Button("Open Pending Changes"))
                NipaWindow.Open();
        }
    }
}
