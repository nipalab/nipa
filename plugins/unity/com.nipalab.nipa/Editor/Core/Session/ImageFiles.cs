using System;
using System.Collections.Generic;
using System.IO;

namespace Nipa.Editor.Core.Session
{
    public static class ImageFiles
    {
        static readonly HashSet<string> Extensions = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".tif", ".tiff", ".webp", ".tga", ".dds", ".ktx2", ".exr", ".hdr", ".psd", ".psb",
        };

        public static bool IsImage(string path)
        {
            return !string.IsNullOrEmpty(path) && Extensions.Contains(Path.GetExtension(path));
        }
    }
}
