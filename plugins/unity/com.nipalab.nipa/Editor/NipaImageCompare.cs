using System.Threading.Tasks;
using Nipa.Editor.Core.Daemon;
using Nipa.Editor.Core.Session;
using UnityEditor;
using UnityEngine;

namespace Nipa.Editor
{
    internal sealed class NipaImageCompare : EditorWindow
    {
        string path;
        string leftLabel;
        string rightLabel;
        string error;
        bool loading;
        int generation;
        ImageCompare data;
        Texture2D left;
        Texture2D right;

        public static void Open(string path, BlobRef oldRef, string oldLabel, BlobRef newRef, string newLabel)
        {
            var w = GetWindow<NipaImageCompare>("Nipa Compare");
            w.minSize = new Vector2(480, 320);
            w.Load(path, oldRef, oldLabel, newRef, newLabel);
            w.Show();
        }

        void Load(string file, BlobRef oldRef, string oldLabel, BlobRef newRef, string newLabel)
        {
            Clear();
            path = file;
            leftLabel = oldLabel;
            rightLabel = newLabel;
            error = null;
            data = null;
            var session = NipaHost.Session;
            if (session == null)
            {
                error = "Not connected to the Nipa daemon.";
                return;
            }
            loading = true;
            var gen = ++generation;
            Task.Run(async () =>
            {
                ImageCompare result = null;
                string failure = null;
                try
                {
                    result = await session.PreviewAsync(oldRef, newRef).ConfigureAwait(false);
                }
                catch (DaemonException e) when (e.Code == 415)
                {
                    failure = "This image format cannot be previewed. Open it in Nipa Desktop or an image editor.";
                }
                catch (System.Exception e)
                {
                    failure = MaintenanceNotes.ErrorText(e);
                }
                MainThread.Post(() =>
                {
                    if (this == null || gen != generation)
                        return;
                    loading = false;
                    error = failure;
                    data = result;
                    if (result != null)
                    {
                        left = Decode(result.Old.Png);
                        right = Decode(result.New.Png);
                    }
                    Repaint();
                });
            });
        }

        static Texture2D Decode(byte[] png)
        {
            if (png == null || png.Length == 0)
                return null;
            var tex = new Texture2D(2, 2, TextureFormat.RGBA32, false) { hideFlags = HideFlags.HideAndDontSave };
            if (ImageConversion.LoadImage(tex, png))
                return tex;
            DestroyImmediate(tex);
            return null;
        }

        void Clear()
        {
            if (left != null)
                DestroyImmediate(left);
            if (right != null)
                DestroyImmediate(right);
            left = right = null;
        }

        void OnDestroy()
        {
            generation++;
            Clear();
        }

        void OnGUI()
        {
            EditorGUILayout.LabelField(path ?? string.Empty, EditorStyles.boldLabel);
            if (loading)
            {
                EditorGUILayout.HelpBox("Loading previews...", MessageType.None);
                return;
            }
            if (error != null)
            {
                EditorGUILayout.HelpBox(error, MessageType.Warning);
                return;
            }
            if (data == null)
                return;
            if (data.Diff != null)
                EditorGUILayout.LabelField(DiffText(data.Diff), EditorStyles.miniLabel);
            using (new EditorGUILayout.HorizontalScope(GUILayout.ExpandHeight(true)))
            {
                DrawSide(leftLabel, data.Old, left);
                DrawSide(rightLabel, data.New, right);
            }
        }

        static void DrawSide(string label, ImageSide side, Texture2D tex)
        {
            using (new EditorGUILayout.VerticalScope(GUILayout.ExpandWidth(true), GUILayout.ExpandHeight(true)))
            {
                EditorGUILayout.LabelField(label, EditorStyles.boldLabel);
                EditorGUILayout.LabelField(InfoText(side.Info), EditorStyles.miniLabel);
                var rect = GUILayoutUtility.GetRect(64, 4096, 64, 4096, GUILayout.ExpandWidth(true), GUILayout.ExpandHeight(true));
                if (tex == null)
                {
                    GUI.Label(rect, side.Info == null ? "No version" : "No preview", EditorStyles.centeredGreyMiniLabel);
                    return;
                }
                var hdr = side.Info != null && side.Info.Hdr;
                float w = side.Info != null && side.Info.PreviewWidth > 0 ? side.Info.PreviewWidth : (hdr ? tex.width / 2 : tex.width);
                float h = side.Info != null && side.Info.PreviewHeight > 0 ? side.Info.PreviewHeight : tex.height;
                var scale = Mathf.Min(rect.width / w, rect.height / h, 1f);
                var fit = new Rect(rect.x + (rect.width - w * scale) / 2, rect.y + (rect.height - h * scale) / 2, w * scale, h * scale);
                EditorGUI.DrawTextureTransparent(fit, Texture2D.clearTexture);
                GUI.DrawTextureWithTexCoords(fit, tex, hdr ? new Rect(0, 0, 0.5f, 1) : new Rect(0, 0, 1, 1), true);
            }
        }

        static string InfoText(ImagePreview info)
        {
            if (info == null)
                return "missing";
            var text = info.Width + "×" + info.Height;
            if (!string.IsNullOrEmpty(info.Format))
                text += " " + info.Format;
            if (!string.IsNullOrEmpty(info.Encoding))
                text += " " + info.Encoding;
            if (info.HasAlpha)
                text += ", alpha";
            if (info.SizeBytes > 0)
                text += ", " + EditorUtility.FormatBytes(info.SizeBytes);
            if (info.Hdr)
                text += ", HDR (shown without exposure)";
            if (info.Thumbnail)
                text += ", embedded thumbnail";
            return text;
        }

        static string DiffText(ImageDiffStats d)
        {
            var text = d.ChangedPixels + " of " + d.TotalPixels + " pixels changed (" + (d.ChangedRatio * 100).ToString("0.##") + "%), max delta " + d.MaxDelta;
            if (d.SizeChanged)
                text += ", size changed";
            if (d.FormatChanged)
                text += ", format changed";
            if (d.BitDepthChanged)
                text += ", bit depth changed";
            if (d.AlphaChanged)
                text += ", alpha changed";
            return text;
        }
    }
}
