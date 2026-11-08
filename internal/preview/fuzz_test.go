package preview

import (
	"os"
	"path/filepath"
	"testing"
)

func addFixtureSeeds(f *testing.F, pattern string) {
	matches, _ := filepath.Glob(filepath.Join("testdata", pattern))
	for _, m := range matches {
		if data, err := os.ReadFile(m); err == nil {
			f.Add(data)
		}
	}
}

func FuzzDecodeTGA(f *testing.F) {
	addFixtureSeeds(f, "*.tga")
	f.Fuzz(func(t *testing.T, data []byte) {
		img, err := DecodeImage("fuzz.tga", data)
		if err == nil && (img.Width() <= 0 || img.Height() <= 0) {
			t.Fatalf("decoded empty image")
		}
	})
}

func FuzzDecodeImage(f *testing.F) {
	addFixtureSeeds(f, "*.png")
	addFixtureSeeds(f, "*.webp")
	f.Fuzz(func(t *testing.T, data []byte) {
		img, err := DecodeImage("fuzz.bin", data)
		if err != nil {
			return
		}
		d := CompareImages(img, img, 0)
		if d.ChangedPixels != 0 {
			t.Fatalf("image differs from itself")
		}
	})
}

func fuzzFormat(f *testing.F, ext, pattern string) {
	addFixtureSeeds(f, pattern)
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, sel := range []Select{{}, {Level: 1, Face: 1}} {
			img, err := DecodeImageAt("fuzz"+ext, data, sel)
			if err == nil && (img.Width() <= 0 || img.Height() <= 0) {
				t.Fatalf("decoded empty image")
			}
		}
	})
}

func FuzzDecodeDDS(f *testing.F)  { fuzzFormat(f, ".dds", "*.dds") }
func FuzzDecodeKTX2(f *testing.F) { fuzzFormat(f, ".ktx2", "*.ktx2") }
func FuzzDecodeEXR(f *testing.F)  { fuzzFormat(f, ".exr", "*.exr") }
func FuzzDecodeHDR(f *testing.F)  { fuzzFormat(f, ".hdr", "*.hdr") }
func FuzzDecodePSD(f *testing.F)  { fuzzFormat(f, ".psd", "*.ps[db]") }

func FuzzDecodeModel(f *testing.F) {
	f.Add([]byte(sampleGLTF), 0)
	f.Add(glbFile(sampleGLTF), 1)
	f.Add([]byte("mtllib a.mtl\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"), 2)
	f.Fuzz(func(t *testing.T, data []byte, kind int) {
		name := []string{"m.gltf", "m.glb", "m.obj"}[uint(kind)%3]
		s, err := DecodeModel(name, data)
		if err == nil && (s.Vertices < 0 || s.Triangles < 0) {
			t.Fatalf("negative counts")
		}
		MaterialLibraryDependencies(data)
	})
}

func FuzzDecodeAudio(f *testing.F) {
	addFixtureSeeds(f, "audio/*")
	f.Add(makeWAV(wavSpec{tag: 1, channels: 2, rate: 8000, bits: 16, width: 2}, sine(8000, 2, 0.05, 300, 0.5)))
	f.Add(makeAIFF("AIFC", "sowt", 1, 1, 8000, 16, sine(8000, 1, 0.05, 300, 0.5)))
	names := []string{"f.wav", "f.aiff", "f.flac", "f.ogg", "f.mp3"}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, name := range names {
			a, err := DecodeAudio(name, data, AudioOptions{Buckets: 64, SpecColumns: 16, SpecRows: 16, PlaybackBytes: 1 << 16})
			if err != nil {
				continue
			}
			if a.Stats.Channels <= 0 || len(a.Peaks) != a.Stats.Channels || len(a.Spectrogram) != a.SpecColumns*a.SpecRows {
				t.Fatalf("%s: inconsistent analysis", name)
			}
		}
	})
}

func FuzzParseTable(f *testing.F) {
	f.Add([]byte("key,text\nhello,\"Hi, there\"\n"))
	f.Add([]byte("a;b\n1;2\n1;2\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		tb, err := ParseTable("f.csv", data)
		if err != nil {
			return
		}
		d := DiffTables(tb, tb)
		if d.RowsAdded+d.RowsRemoved+d.RowsChanged+d.ColumnsAdded+d.ColumnsRemoved != 0 {
			t.Fatalf("table differs from itself")
		}
		if e := DiffTables(nil, tb); e.RowsAdded != max(len(tb.Rows)-1, 0) {
			t.Fatalf("added %d rows from %d", e.RowsAdded, len(tb.Rows))
		}
	})
}

func FuzzParseUnityYAML(f *testing.F) {
	addFixtureSeeds(f, "unity/*")
	f.Add([]byte("%YAML 1.1\n--- !u!1 &1 stripped\nGameObject:\n  m_PrefabInstance: {fileID: 1}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ParseUnityYAML(data, func(g string) string { return "Assets/" + g })
		if err != nil {
			return
		}
		if d := DiffScenes(s, s); len(d.Changes) != 0 {
			t.Fatalf("scene differs from itself: %+v", d.Changes[0])
		}
		if d := DiffScenes(nil, s); d.Added != len(s.Objects) {
			t.Fatalf("added %d of %d objects", d.Added, len(s.Objects))
		}
	})
}

func FuzzParseGodotResource(f *testing.F) {
	addFixtureSeeds(f, "godot/*")
	f.Add([]byte("[node name=\"A\" parent=\".\" instance=ExtResource(\"1\")]\nx = SubResource(\"2\")\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ParseGodotResource(data)
		if err != nil {
			return
		}
		if d := DiffScenes(s, s); len(d.Changes) != 0 {
			t.Fatalf("scene differs from itself: %+v", d.Changes[0])
		}
		if d := DiffScenes(nil, s); d.Added != len(s.Objects) {
			t.Fatalf("added %d of %d objects", d.Added, len(s.Objects))
		}
	})
}
