using System;
using System.IO;
using Newtonsoft.Json;

namespace Nipa.Editor.Core.Session
{
    public sealed class NipaSettings
    {
        public int PollIntervalSeconds { get; set; } = 60;

        public static NipaSettings Load(string path)
        {
            try
            {
                var s = JsonConvert.DeserializeObject<NipaSettings>(File.ReadAllText(path));
                return Normalize(s ?? new NipaSettings());
            }
            catch (Exception e) when (e is IOException || e is JsonException || e is UnauthorizedAccessException)
            {
                return new NipaSettings();
            }
        }

        public void Save(string path)
        {
            Directory.CreateDirectory(Path.GetDirectoryName(Path.GetFullPath(path)));
            File.WriteAllText(path, JsonConvert.SerializeObject(Normalize(this), Formatting.Indented));
        }

        static NipaSettings Normalize(NipaSettings s)
        {
            if (s.PollIntervalSeconds < 15)
                s.PollIntervalSeconds = 15;
            if (s.PollIntervalSeconds > 3600)
                s.PollIntervalSeconds = 3600;
            return s;
        }
    }
}
