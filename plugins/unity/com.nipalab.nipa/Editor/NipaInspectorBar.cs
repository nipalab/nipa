using Nipa.Editor.Core.Locks;
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
                GUILayout.Label(Label(info), EditorStyles.miniBoldLabel);
                GUILayout.FlexibleSpace();
                using (new EditorGUI.DisabledScope(NipaHost.Busy != null))
                {
                    if (info.State == LockState.Free && GUILayout.Button("Lock", EditorStyles.miniButton))
                        NipaLocks.Lock(new System.Collections.Generic.List<string> { clone });
                    if (info.State == LockState.Mine && GUILayout.Button("Unlock", EditorStyles.miniButton))
                        NipaLocks.Unlock(new System.Collections.Generic.List<string> { clone });
                }
            }
        }

        static string Label(LockInfo info)
        {
            switch (info.State)
            {
                case LockState.Mine:
                    return "Nipa: locked by you";
                case LockState.Theirs:
                    return "Nipa: locked by " + info.Holder + " (read-only)";
                default:
                    return "Nipa: not locked";
            }
        }
    }
}
