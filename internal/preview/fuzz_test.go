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
