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
