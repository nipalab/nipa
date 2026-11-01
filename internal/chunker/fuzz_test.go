package chunker

import (
	"bytes"
	"testing"
)

var fuzzPaths = []string{"notes.txt", "texture.png", "scene.blend", "blob.bin", "archive.zip"}

// FuzzEncodeDecodeRoundTrip checks that whatever bytes and file name go in,
// decoding the stored chunks in order reproduces the input and every chunk hash
// refers to the stored bytes.
func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	f.Add([]byte(""), uint8(0))
	f.Add([]byte("hello\nworld\n"), uint8(0))
	f.Add([]byte{0, 1, 2, 3, 4, 5}, uint8(1))
	f.Add(bytes.Repeat([]byte("line of text\n"), 5000), uint8(0))
	f.Add(bytes.Repeat([]byte{7, 1, 9, 200}, 20000), uint8(2))
	f.Add(append([]byte{0}, bytes.Repeat([]byte("mixed"), 3000)...), uint8(3))

	f.Fuzz(func(t *testing.T, data []byte, pathSel uint8) {
		path := fuzzPaths[int(pathSel)%len(fuzzPaths)]
		isBinary := IsBinary(data)

		encoding, chunks, err := EncodeBytes(data, path, isBinary, "")
		if err != nil {
			t.Fatalf("encode %q: %v", path, err)
		}

		var decoded []byte
		for i, c := range chunks {
			if c.Hash != Sum(c.Data) {
				t.Fatalf("chunk %d hash does not match its stored bytes", i)
			}
			plain, err := Decode(encoding, c.Data)
			if err != nil {
				t.Fatalf("decode chunk %d: %v", i, err)
			}
			decoded = append(decoded, plain...)
		}
		if !bytes.Equal(decoded, data) {
			t.Fatalf("round trip mismatch: got %d bytes, want %d (encoding %s)", len(decoded), len(data), encoding)
		}

		_, again, err := EncodeBytes(data, path, isBinary, encoding)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if len(again) != len(chunks) {
			t.Fatalf("encoding is not deterministic: %d chunks then %d", len(chunks), len(again))
		}
		for i := range chunks {
			if again[i].Hash != chunks[i].Hash {
				t.Fatalf("chunk %d hash differs between identical encodes", i)
			}
		}
	})
}
