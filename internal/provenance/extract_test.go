package provenance

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func extractBytes(t *testing.T, name string, data []byte) (*Record, bool, error) {
	t.Helper()
	return Extract(name, bytes.NewReader(data), Limits{})
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func TestExtract_A1111(t *testing.T) {
	rec, ok, err := extractBytes(t, "hero.PNG", readFixture(t, "a1111.png"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Automatic1111", rec.Tool)
	require.Equal(t, "sdxl_base_1.0", rec.Model)
	require.Equal(t, uint64(123456789), *rec.Seed)
	require.Equal(t, PromptHash("a castle on a hill, golden hour"), rec.PromptSHA256)
	require.Equal(t, SourcePNGText, rec.Source)
	require.Empty(t, rec.ContentHash)
}

func TestParseA1111_Variants(t *testing.T) {
	rec := parseA1111("only a prompt")
	require.Equal(t, PromptHash("only a prompt"), rec.PromptSHA256)
	require.Nil(t, rec.Seed)

	rec = parseA1111("Steps: 4, Seed: 99999999999999999999999, Model: m")
	require.Empty(t, rec.PromptSHA256)
	require.Nil(t, rec.Seed)
	require.Equal(t, "m", rec.Model)

	rec = parseA1111("multi\r\nline\r\nSteps: 4, Seed: 5")
	require.Equal(t, PromptHash("multi\nline"), rec.PromptSHA256)
	require.Equal(t, uint64(5), *rec.Seed)
}

func TestExtract_ComfyUI(t *testing.T) {
	rec, ok, err := extractBytes(t, "a.png", readFixture(t, "comfyui.png"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ComfyUI", rec.Tool)
	require.Equal(t, "sdxl_base_1.0.safetensors", rec.Model)
	require.Equal(t, uint64(987654321), *rec.Seed)
	require.Equal(t, PromptHash("a red dragon"), rec.PromptSHA256)
}

func TestParseComfyUI_Variants(t *testing.T) {
	require.Nil(t, parseComfyUI("not json"))
	require.Nil(t, parseComfyUI("{}"))
	require.Nil(t, parseComfyUI(`{"1": {"inputs": {}}}`))

	chained := `{
 "a": {"class_type": "CLIPTextEncode", "inputs": {"text": ["p", 0]}},
 "10": {"class_type": "KSamplerAdvanced", "inputs": {"noise_seed": 42, "positive": [5, 0]}},
 "2": {"class_type": "KSampler", "inputs": {"seed": "x", "positive": ["5", 0]}},
 "5": {"class_type": "ConditioningCombine", "inputs": {"conditioning_1": ["missing", 0], "conditioning_2": ["6", 0], "c3": ["a", 0]}},
 "6": {"class_type": "CLIPTextEncodeSDXL", "inputs": {"text": "hello"}},
 "p": {"class_type": "PrimitiveNode", "inputs": {"value": "x"}}
}`
	rec := parseComfyUI(chained)
	require.NotNil(t, rec)
	require.Nil(t, rec.Seed, "the first sampler (id 2) has no numeric seed")
	require.Equal(t, PromptHash("hello"), rec.PromptSHA256)
	require.Empty(t, rec.Model)

	numeric := `{"1": {"class_type": "KSampler", "inputs": {"noise_seed": 7, "positive": [3, 0]}}, "3": {"class_type": "CLIPTextEncode", "inputs": {"text": "x"}}}`
	rec = parseComfyUI(numeric)
	require.Equal(t, uint64(7), *rec.Seed)
	require.Equal(t, PromptHash("x"), rec.PromptSHA256)

	_, ok := comfyPositive(nil, []byte(`"nope"`))
	require.False(t, ok)
	_, ok = comfyLink([]byte(`[true, 0]`))
	require.False(t, ok)
	require.True(t, nodeIDLess("2", "10"))
	require.True(t, nodeIDLess("2", "a"))
	require.False(t, nodeIDLess("a", "2"))
	require.True(t, nodeIDLess("a", "b"))
}

func TestExtract_C2PAPNG(t *testing.T) {
	rec, ok, err := extractBytes(t, "a.png", readFixture(t, "c2pa.png"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Firefly", rec.Tool)
	require.Equal(t, SourceC2PA, rec.Source)
	require.NotNil(t, rec.C2PAVerified)
	require.False(t, *rec.C2PAVerified)
	require.Equal(t, trainedMedia, rec.DigitalSourceType)
	require.NoError(t, rec.Validate())
}

func TestExtract_C2PAJPEG(t *testing.T) {
	data := readFixture(t, "c2pa.jpg")
	rec, ok, err := extractBytes(t, "a.jpeg", data)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Firefly", rec.Tool)
	require.Equal(t, trainedMedia, rec.DigitalSourceType)

	single := jpegWithJUMBF(c2paStore(map[string]any{"claim_generator": "Gen\x01AI/2.0"}, nil), 60000)
	rec, ok, err = extractBytes(t, "a.jpg", single)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Gen AI/2.0", rec.Tool)
	require.Empty(t, rec.DigitalSourceType)
}

func TestExtract_TextWinsOverC2PA(t *testing.T) {
	data := withChunks(tinyPNG(), textChunk("parameters", a1111Params), pngChunk("caBX", defaultC2PA()))
	rec, ok, err := extractBytes(t, "a.png", data)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Automatic1111", rec.Tool)
	require.Equal(t, trainedMedia, rec.DigitalSourceType)

	broken := withChunks(tinyPNG(), pngChunk("caBX", []byte{0, 0, 0, 3}))
	_, ok, err = extractBytes(t, "a.png", broken)
	require.False(t, ok)
	require.ErrorIs(t, err, errJUMBF)
}

func TestExtract_PlainAndUnsupported(t *testing.T) {
	rec, ok, err := extractBytes(t, "a.png", readFixture(t, "plain.png"))
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, rec)

	_, ok, err = extractBytes(t, "a.jpg", tinyJPEG())
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = extractBytes(t, "a.txt", []byte("hello"))
	require.NoError(t, err)
	require.False(t, ok)
	require.True(t, Supported("A.JPG"))
	require.False(t, Supported("a.webp"))
}

func TestExtract_TruncatedAndInvalid(t *testing.T) {
	full := readFixture(t, "a1111.png")
	for _, n := range []int{0, 7, 12, 40, len(full) - 13} {
		_, ok, err := extractBytes(t, "a.png", full[:n])
		require.False(t, ok)
		require.Error(t, err, n)
	}
	_, _, err := extractBytes(t, "a.png", []byte("GIF89a..."))
	require.ErrorIs(t, err, ErrNotPNG)

	bad := append([]byte(nil), pngSignature...)
	bad = append(bad, 0xff, 0xff, 0xff, 0xff, 'I', 'D', 'A', 'T')
	_, _, err = extractBytes(t, "a.png", bad)
	require.ErrorContains(t, err, "invalid chunk length")

	jpg := readFixture(t, "c2pa.jpg")
	for _, n := range []int{1, 3, 5, 30} {
		_, ok, err := extractBytes(t, "a.jpg", jpg[:n])
		require.False(t, ok)
		require.Error(t, err, n)
	}
	_, _, err = extractBytes(t, "a.jpg", []byte{0xFF, 0xD8, 0x00})
	require.ErrorContains(t, err, "expected a marker")
	_, _, err = extractBytes(t, "a.jpg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x01})
	require.ErrorContains(t, err, "invalid segment length")
	_, _, err = extractBytes(t, "a.jpg", []byte("nope"))
	require.ErrorIs(t, err, ErrNotJPEG)
	_, ok, err := extractBytes(t, "a.jpg", []byte{0xFF, 0xD8, 0xFF, 0xFF, 0xD0, 0xFF, 0x01, 0xFF, 0xEB, 0x00, 0x05, 'X', 'Y', 'Z', 0xFF, 0xD9})
	require.NoError(t, err)
	require.False(t, ok)

	garbled := jpegWithJUMBF(rawBox("jumb", []byte{1, 2, 3}), 100)
	_, ok, err = extractBytes(t, "a.jpg", garbled)
	require.False(t, ok)
	require.ErrorIs(t, err, errJUMBF)
}

func TestWalkJPEG_Limits(t *testing.T) {
	store := defaultC2PA()
	_, err := walkJPEG(bytes.NewReader(jpegWithJUMBF(store, 100)), Limits{MaxStore: 64}.withDefaults())
	require.ErrorContains(t, err, "too large")

	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xD8})
	for range maxJPEGPackets + 1 {
		b.Write([]byte{0xFF, 0xEB, 0, 10, 'J', 'P', 0, 1, 0, 0, 0, 1})
	}
	_, err = walkJPEG(bytes.NewReader(b.Bytes()), Limits{}.withDefaults())
	require.ErrorContains(t, err, "too many APP11")

	_, ok := assemble([]jpegPacket{{seq: 1, data: []byte{1}}, {seq: 2, data: []byte{0, 0, 0, 1, 'j'}}})
	require.False(t, ok)
	got, ok := assemble([]jpegPacket{{seq: 2, data: append(binary.BigEndian.AppendUint32(nil, 1), make([]byte, 12)...)}, {seq: 1, data: []byte("head")}})
	require.True(t, ok)
	require.Equal(t, "head", string(got))
}

func TestPNGText_Variants(t *testing.T) {
	lim := Limits{}.withDefaults()
	cases := []struct {
		chunk []byte
		key   string
		text  string
		ok    bool
	}{
		{textChunk("parameters", "caf\xe9"), "parameters", "café", true},
		{zTextChunk("prompt", []byte("{}")), "prompt", "{}", true},
		{iTextChunk("prompt", "ünïcode", false), "prompt", "ünïcode", true},
		{iTextChunk("prompt", "packed", true), "prompt", "packed", true},
		{pngChunk("tEXt", []byte("nokeyterminator")), "", "", false},
		{pngChunk("tEXt", []byte("\x00value")), "", "", false},
		{pngChunk("zTXt", []byte("k\x00\x01xx")), "", "", false},
		{pngChunk("zTXt", []byte("k\x00\x00notzlib")), "", "", false},
		{pngChunk("iTXt", []byte("k\x00\x01")), "", "", false},
		{pngChunk("iTXt", []byte("k\x00\x00\x00en")), "", "", false},
		{pngChunk("iTXt", []byte("k\x00\x01\x01en\x00\x00x")), "", "", false},
		{pngChunk("iTXt", []byte("k\x00\x01\x00en\x00\x00bad")), "", "", false},
		{pngChunk("iTXt", []byte("k\x00\x00\x00en\x00\x00\xff\xfe")), "", "", false},
	}
	for i, tc := range cases {
		data := tc.chunk[8 : len(tc.chunk)-4]
		key, text, ok := pngText(string(tc.chunk[4:8]), data, lim.MaxInflate, new(int64))
		require.Equal(t, tc.ok, ok, i)
		require.Equal(t, tc.key, key, i)
		require.Equal(t, tc.text, text, i)
	}
}

func TestExtract_SkipsOversizedTextChunk(t *testing.T) {
	data := withChunks(tinyPNG(), textChunk("parameters", a1111Params))
	_, ok, err := Extract("a.png", bytes.NewReader(data), Limits{MaxChunk: 16})
	require.NoError(t, err)
	require.False(t, ok)
}

type failingSeeker struct{ io.Reader }

func (failingSeeker) Seek(int64, int) (int64, error) { return 0, io.ErrClosedPipe }

func TestExtract_SeekErrors(t *testing.T) {
	_, _, err := Extract("a.png", failingSeeker{bytes.NewReader(tinyPNG())}, Limits{})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	_, _, err = Extract("a.png", failingSeeker{bytes.NewReader(readFixture(t, "a1111.png")[:8+25+12+20])}, Limits{})
	require.Error(t, err)
	_, _, err = Extract("a.jpg", failingSeeker{bytes.NewReader(tinyJPEG())}, Limits{})
	require.ErrorIs(t, err, io.ErrClosedPipe)
}
