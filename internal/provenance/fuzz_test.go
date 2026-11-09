package provenance

import (
	"bytes"
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

func FuzzParseFile(f *testing.F) {
	f.Add([]byte(sampleFile))
	f.Add([]byte("version: 1\nfiles: {}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(data)
		if err != nil {
			return
		}
		out := file.Marshal()
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("marshal output does not parse: %v\n%s", err, out)
		}
		if !bytes.Equal(out, again.Marshal()) {
			t.Fatalf("marshal is not stable")
		}
	})
}

func FuzzExtractPNG(f *testing.F) {
	addFixtureSeeds(f, "*.png")
	f.Fuzz(func(t *testing.T, data []byte) {
		rec, ok, _ := Extract("fuzz.png", bytes.NewReader(data), Limits{})
		if ok && rec.Validate() != nil {
			t.Fatalf("extracted an invalid record: %+v", rec)
		}
	})
}

func FuzzExtractJPEG(f *testing.F) {
	addFixtureSeeds(f, "*.jpg")
	f.Fuzz(func(t *testing.T, data []byte) {
		rec, ok, _ := Extract("fuzz.jpg", bytes.NewReader(data), Limits{})
		if ok && rec.Validate() != nil {
			t.Fatalf("extracted an invalid record: %+v", rec)
		}
	})
}

func FuzzParseC2PA(f *testing.F) {
	f.Add(defaultC2PA())
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = parseC2PA(data, Limits{}.withDefaults())
	})
}

func FuzzDecodeCBOR(f *testing.F) {
	f.Add(cborEncode(map[string]any{"claim_generator": "x", "a": []any{uint64(1), -2, true, nil}}))
	f.Add([]byte{0xf9, 0x3c, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = decodeCBOR(data, defaultCBORDepth)
	})
}
