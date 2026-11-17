using System;
using UnityEditor;
using UnityEngine;

namespace Nipa.Editor
{
    internal sealed class NipaPrompt : EditorWindow
    {
        const string Control = "nipa-prompt";
        string label;
        string value;
        string ok;
        bool allowEmpty;
        bool focused;
        Action<string> done;

        public static void Ask(string title, string label, string initial, string ok, bool allowEmpty, Action<string> done)
        {
            var w = CreateInstance<NipaPrompt>();
            w.titleContent = new GUIContent(title);
            w.label = label;
            w.value = initial ?? string.Empty;
            w.ok = ok;
            w.allowEmpty = allowEmpty;
            w.done = done;
            w.minSize = new Vector2(380, 110);
            w.maxSize = new Vector2(640, 110);
            w.ShowUtility();
            w.Focus();
        }

        void OnGUI()
        {
            var e = Event.current;
            var submit = e.type == EventType.KeyDown && (e.keyCode == KeyCode.Return || e.keyCode == KeyCode.KeypadEnter);
            var cancel = e.type == EventType.KeyDown && e.keyCode == KeyCode.Escape;
            GUILayout.Label(label, EditorStyles.wordWrappedLabel);
            GUI.SetNextControlName(Control);
            value = EditorGUILayout.TextField(value);
            if (!focused)
            {
                EditorGUI.FocusTextInControl(Control);
                focused = true;
            }
            var valid = allowEmpty || !string.IsNullOrWhiteSpace(value);
            GUILayout.FlexibleSpace();
            using (new EditorGUILayout.HorizontalScope())
            {
                GUILayout.FlexibleSpace();
                if (GUILayout.Button("Cancel", GUILayout.Width(80)))
                    cancel = true;
                using (new EditorGUI.DisabledScope(!valid))
                {
                    if (GUILayout.Button(ok, GUILayout.MinWidth(80)))
                        submit = true;
                }
            }
            if (cancel)
            {
                done = null;
                Close();
                GUIUtility.ExitGUI();
            }
            else if (submit && valid)
            {
                var callback = done;
                var result = value.Trim();
                done = null;
                Close();
                callback?.Invoke(result);
                GUIUtility.ExitGUI();
            }
        }
    }
}
