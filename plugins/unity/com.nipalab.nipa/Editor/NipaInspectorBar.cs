using Nipa.Editor.Core.Locks;
using Nipa.Editor.Core.Paths;
using UnityEditor;
using UnityEngine;

namespace Nipa.Editor
{
    [InitializeOnLoad]
    internal static class NipaInspectorBar
    {
        static NipaInspectorBar()
        {
            Editor.finishedDefaultHeaderGUI += Draw;
        }

        static void Draw(Editor editor)
        {
            var session = NipaHost.Session;
            if (session == null || editor == null || editor.target == null)
                return;
            var path = AssetDatabase.GetAssetPath(editor.target);
            if (!NipaHost.IsManaged(path) || path == "Assets")
                return;
            var clone = session.Paths.ToClonePath(path);
            var info = session.Locks.Lookup(clone);
            using (new EditorGUILayout.HorizontalScope(EditorStyles.helpBox))
            {
                GUILayout.Label(Label(info, clone), EditorStyles.miniBoldLabel);
                GUILayout.FlexibleSpace();
                using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
                {
                    if (info.State == LockState.Free && GUILayout.Button("Lock", EditorStyles.miniButton))
                        NipaLocks.Lock(new System.Collections.Generic.List<string> { clone });
                    var own = info.State == LockState.Mine && ProjectPaths.AssetOf(info.LockPath) == clone;
                    if (own && GUILayout.Button("Unlock", EditorStyles.miniButton))
                        NipaLocks.Unlock(new System.Collections.Generic.List<string> { clone });
                }
            }
        }

        static string Label(LockInfo info, string clone)
        {
            switch (info.State)
            {
                case LockState.Mine:
                    return ProjectPaths.AssetOf(info.LockPath) == clone ? "Nipa: locked by you" : "Nipa: locked by you (" + info.LockPath + ")";
                case LockState.Theirs:
                    return "Nipa: locked by " + info.Holder + " (read-only)";
                default:
                    return "Nipa: not locked";
            }
        }
    }
}
