using Nipa.Editor.Core.Status;
using UnityEngine;

namespace Nipa.Editor
{
    internal static class NipaIcons
    {
        const int Size = 16;

        static Texture2D conflict, modified, removed, staged, untracked, lockMine, lockTheirs, behind;

        public static Texture2D For(FileState state)
        {
            if (state.HasFlag(FileState.Conflict))
                return conflict = conflict != null ? conflict : Dot(new Color32(0xEF, 0x44, 0x44, 0xFF));
            if (state.HasFlag(FileState.Modified))
                return modified = modified != null ? modified : Dot(new Color32(0xF5, 0x9E, 0x0B, 0xFF));
            if ((state & (FileState.Deleted | FileState.Missing)) != 0)
                return removed = removed != null ? removed : Dot(new Color32(0xA8, 0x55, 0xF7, 0xFF));
            if ((state & (FileState.Staged | FileState.Added)) != 0)
                return staged = staged != null ? staged : Dot(new Color32(0x22, 0xC5, 0x5E, 0xFF));
            if (state.HasFlag(FileState.Untracked))
                return untracked = untracked != null ? untracked : Dot(new Color32(0x60, 0xA5, 0xFA, 0xFF));
            return null;
        }

        public static Texture2D LockMine => lockMine = lockMine != null ? lockMine : Shape(new Color32(0x14, 0xB8, 0xA6, 0xFF), Square);

        public static Texture2D LockTheirs => lockTheirs = lockTheirs != null ? lockTheirs : Shape(new Color32(0xEF, 0x44, 0x44, 0xFF), Square);

        public static Texture2D Behind => behind = behind != null ? behind : Shape(new Color32(0x06, 0xB6, 0xD4, 0xFF), Triangle);

        static float Square(float x, float y, float c)
        {
            return Mathf.Clamp01(c - Mathf.Max(Mathf.Abs(x - c), Mathf.Abs(y - c)) + 0.5f);
        }

        static float Triangle(float x, float y, float c)
        {
            var half = y / 2f;
            return Mathf.Clamp01(half - Mathf.Abs(x - c) + 0.5f);
        }

        static Texture2D Dot(Color color)
        {
            return Shape(color, (x, y, c) => Mathf.Clamp01(c + 0.5f - Mathf.Sqrt((x - c) * (x - c) + (y - c) * (y - c))));
        }

        static Texture2D Shape(Color color, System.Func<float, float, float, float> coverage)
        {
            var tex = new Texture2D(Size, Size, TextureFormat.RGBA32, false)
            {
                hideFlags = HideFlags.HideAndDontSave,
                filterMode = FilterMode.Bilinear,
            };
            var c = (Size - 1) / 2f;
            for (var y = 0; y < Size; y++)
            {
                for (var x = 0; x < Size; x++)
                {
                    var alpha = coverage(x, y, c);
                    var edge = alpha < 1f ? 0.55f : 1f;
                    tex.SetPixel(x, y, new Color(color.r * edge, color.g * edge, color.b * edge, alpha));
                }
            }
            tex.Apply();
            return tex;
        }
    }
}
