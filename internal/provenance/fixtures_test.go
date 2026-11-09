package provenance

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	a1111Params = "a castle on a hill, golden hour\nNegative prompt: blurry\nSteps: 20, Sampler: Euler a, CFG scale: 7, Seed: 123456789, Size: 512x512, Model hash: abc123, Model: sdxl_base_1.0"
	comfyPrompt = `{
 "3": {"class_type": "KSampler", "inputs": {"seed": 987654321, "positive": ["6", 0], "negative": ["7", 0], "model": ["4", 0]}},
 "4": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "sdxl_base_1.0.safetensors"}},
 "6": {"class_type": "CLIPTextEncode", "inputs": {"text": "a red dragon", "clip": ["4", 1]}},
 "7": {"class_type": "CLIPTextEncode", "inputs": {"text": "low quality", "clip": ["4", 1]}}
}`
	aiSourceURL = "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"
)

func tinyPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, A: 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func pngChunk(typ string, data []byte) []byte {
	out := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(out, uint32(len(data)))
	copy(out[4:], typ)
	out = append(out, data...)
	crc := crc32.ChecksumIEEE(append([]byte(typ), data...))
	return binary.BigEndian.AppendUint32(out, crc)
}

// withChunks inserts chunks right after the IHDR chunk of a PNG.
func withChunks(base []byte, chunks ...[]byte) []byte {
	ihdrEnd := 8 + 8 + 13 + 4
	out := append([]byte(nil), base[:ihdrEnd]...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, base[ihdrEnd:]...)
}

func textChunk(key, value string) []byte {
	return pngChunk("tEXt", append(append([]byte(key), 0), value...))
}

func zlibBytes(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func zTextChunk(key string, value []byte) []byte {
	return pngChunk("zTXt", append(append([]byte(key), 0, 0), zlibBytes(value)...))
}

func iTextChunk(key, value string, compressed bool) []byte {
	data := append([]byte(key), 0)
	body := []byte(value)
	if compressed {
		data = append(data, 1, 0)
		body = zlibBytes(body)
	} else {
		data = append(data, 0, 0)
	}
	data = append(data, "en"...)
	data = append(data, 0, 0)
	return pngChunk("iTXt", append(data, body...))
}

func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n <= 0xff:
		return []byte{major<<5 | 24, byte(n)}
	case n <= 0xffff:
		return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
	case n <= 0xffffffff:
		return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
	}
	return binary.BigEndian.AppendUint64([]byte{major<<5 | 27}, n)
}

func cborEncode(v any) []byte {
	switch x := v.(type) {
	case uint64:
		return cborHead(0, x)
	case int:
		if x < 0 {
			return cborHead(1, uint64(-1-x))
		}
		return cborHead(0, uint64(x))
	case string:
		return append(cborHead(3, uint64(len(x))), x...)
	case []byte:
		return append(cborHead(2, uint64(len(x))), x...)
	case bool:
		if x {
			return []byte{0xf5}
		}
		return []byte{0xf4}
	case nil:
		return []byte{0xf6}
	case []any:
		out := cborHead(4, uint64(len(x)))
		for _, e := range x {
			out = append(out, cborEncode(e)...)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := cborHead(5, uint64(len(x)))
		for _, k := range keys {
			out = append(out, cborEncode(k)...)
			out = append(out, cborEncode(x[k])...)
		}
		return out
	}
	panic("unsupported cbor value")
}

func rawBox(typ string, payload []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	out = append(out, typ...)
	return append(out, payload...)
}

func jumbBox(label string, content ...[]byte) []byte {
	d := make([]byte, 16, 16+2+len(label))
	d = append(d, 0x03)
	d = append(d, label...)
	d = append(d, 0)
	payload := rawBox("jumd", d)
	for _, c := range content {
		payload = append(payload, c...)
	}
	return rawBox("jumb", payload)
}

func c2paStore(claim map[string]any, actions map[string]any) []byte {
	assertions := [][]byte{jumbBox("c2pa.hash.data", rawBox("cbor", cborEncode(map[string]any{"alg": "sha256"})))}
	if actions != nil {
		assertions = append(assertions, jumbBox("c2pa.actions.v2", rawBox("cbor", cborEncode(actions))))
	}
	old := jumbBox("urn:uuid:old", jumbBox("c2pa.claim", rawBox("cbor", cborEncode(map[string]any{"claim_generator": "Old/1.0"}))))
	active := jumbBox("urn:uuid:active",
		jumbBox("c2pa.assertions", assertions...),
		jumbBox("c2pa.claim.v2", rawBox("cbor", cborEncode(claim))),
		jumbBox("c2pa.signature", rawBox("cbor", cborEncode([]byte{1, 2, 3}))),
	)
	return jumbBox("c2pa", old, active)
}

func defaultC2PA() []byte {
	return c2paStore(
		map[string]any{"claim_generator_info": []any{map[string]any{"name": "Firefly", "version": "3"}}},
		map[string]any{"actions": []any{map[string]any{"action": "c2pa.created", "digitalSourceType": aiSourceURL}}},
	)
}

func tinyJPEG() []byte {
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

// jpegWithJUMBF splits store into APP11 packets of at most chunk box bytes.
func jpegWithJUMBF(store []byte, chunk int) []byte {
	base := tinyJPEG()
	out := append([]byte(nil), base[:2]...)
	header, body := store[:8], store[8:]
	seq := uint32(1)
	for first := true; first || len(body) > 0; first = false {
		n := min(chunk, len(body))
		pkt := []byte{'J', 'P', 0, 7}
		pkt = binary.BigEndian.AppendUint32(pkt, seq)
		pkt = append(pkt, header...)
		pkt = append(pkt, body[:n]...)
		body = body[n:]
		seg := []byte{0xFF, 0xEB}
		seg = binary.BigEndian.AppendUint16(seg, uint16(len(pkt)+2))
		out = append(out, append(seg, pkt...)...)
		seq++
	}
	return append(out, base[2:]...)
}

func fixtureFiles() map[string][]byte {
	return map[string][]byte{
		"plain.png":   tinyPNG(),
		"a1111.png":   withChunks(tinyPNG(), textChunk("parameters", a1111Params)),
		"comfyui.png": withChunks(tinyPNG(), textChunk("prompt", comfyPrompt), textChunk("workflow", "{}")),
		"c2pa.png":    withChunks(tinyPNG(), pngChunk("caBX", defaultC2PA())),
		"c2pa.jpg":    jpegWithJUMBF(defaultC2PA(), 200),
	}
}

// TestWriteFixtures regenerates testdata (NIPA_PROVENANCE_GEN=1).
func TestWriteFixtures(t *testing.T) {
	if os.Getenv("NIPA_PROVENANCE_GEN") == "" {
		t.Skip("set NIPA_PROVENANCE_GEN=1 to regenerate testdata")
	}
	for name, data := range fixtureFiles() {
		require.NoError(t, os.WriteFile(filepath.Join("testdata", name), data, 0o644))
	}
}
