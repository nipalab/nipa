package provenance

import (
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
)

const sampleFile = `version: 1
files:
  Art/hero.png:
    content_hash: blake3:9f2c000000000000000000000000000000000000000000000000000000000000
    tool: ComfyUI
    model: sdxl_base_1.0.safetensors
    prompt_sha256: 4b1e000000000000000000000000000000000000000000000000000000000000
    seed: 123456789
    license: CreativeML-OpenRAIL-M
    human_edited: false
    source: png-text
    recorded_at: 2026-11-09T10:00:00Z
  /Audio\voice.wav:
    tool: ElevenLabs
    human_edited: true
    source: manual
    note: "cleaned up: by hand"
`

func TestParseMarshalRoundTrip(t *testing.T) {
	f, err := Parse([]byte(sampleFile))
	require.NoError(t, err)
	require.Equal(t, []string{"Art/hero.png", "Audio/voice.wav"}, f.Paths())
	hero := f.Get("Art/hero.png")
	require.Equal(t, "ComfyUI", hero.Tool)
	require.Equal(t, uint64(123456789), *hero.Seed)
	require.Equal(t, time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), hero.RecordedAt.UTC())

	out := f.Marshal()
	require.True(t, strings.HasPrefix(string(out), "version: 1\nfiles:\n  Art/hero.png:\n    content_hash: blake3:9f2c"))
	require.Contains(t, string(out), "  Audio/voice.wav:\n    tool: ElevenLabs\n    human_edited: true\n    source: manual\n    note: 'cleaned up: by hand'\n")
	again, err := Parse(out)
	require.NoError(t, err)
	require.Equal(t, out, again.Marshal())
	require.Equal(t, f.Files["Audio/voice.wav"], again.Files["Audio/voice.wav"])
}

func TestMarshalEmptyAndNilRecord(t *testing.T) {
	f := New()
	require.Equal(t, "version: 1\nfiles: {}\n", string(f.Marshal()))
	parsed, err := Parse(f.Marshal())
	require.NoError(t, err)
	require.Empty(t, parsed.Files)

	nilRec, err := Parse([]byte("version: 1\nfiles:\n  a.png:\n"))
	require.NoError(t, err)
	require.NotNil(t, nilRec.Files["a.png"])
	f.Files["b.png"] = nil
	require.Contains(t, string(f.Marshal()), "b.png:\n    human_edited: false\n")
}

func TestMarshalC2PA(t *testing.T) {
	f := New()
	v := false
	require.NoError(t, f.Set("x.jpg", &Record{Tool: "Firefly", Source: SourceC2PA, C2PAVerified: &v}))
	out := string(f.Marshal())
	require.Contains(t, out, "c2pa_verified: false")
	_, err := Parse(f.Marshal())
	require.NoError(t, err)
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"version: 2\n":           "unsupported version 2",
		"files: {}\n":            "unsupported version 0",
		"version: 1\nextra: 1\n": "field extra not found",
		"version: 1\nfiles:\n  a.png:\n    color: red\n":   "field color not found",
		"version: 1\nfiles:\n  a.png:\n    source: ai\n":   `unknown source "ai"`,
		"version: 1\nfiles:\n  a.png:\n    seed: -1\n":     "cannot unmarshal",
		"version: 1\nfiles:\n  ../a.png: {}\n":             "..",
		"version: 1\nfiles:\n  /: {}\n":                    "invalid path",
		"version: 1\nfiles:\n  a.png: {}\n  /a.png: {}\n":  "duplicate record",
		"version: 1\nfiles: [\n":                           "did not find",
		"version: 1\nfiles:\n  a: {content_hash: md5:1}\n": "invalid content_hash",
		"version: 1\nfiles:\n  a: {prompt_sha256: xyz}\n":  "invalid prompt_sha256",
		"version: 1\nfiles:\n  a: {c2pa_verified: true}\n": "c2pa_verified requires source",
	}
	for in, want := range cases {
		_, err := Parse([]byte(in))
		require.Error(t, err, in)
		require.Contains(t, err.Error(), want, in)
	}
	_, err := Parse(nil)
	require.ErrorContains(t, err, "unsupported version 0")
}

func TestSetGetDeleteMove(t *testing.T) {
	f := &File{}
	require.Error(t, f.Set("", &Record{}))
	require.Error(t, f.Set("a.png", nil))
	require.Error(t, f.Set("a.png", &Record{Source: "bogus"}))
	require.NoError(t, f.Set("dir\\a.png", &Record{Tool: "x"}))
	require.Equal(t, 1, f.Version)
	require.Equal(t, "x", f.Get("/dir/a.png").Tool)
	require.Nil(t, f.Get("../x"))

	require.True(t, f.Move("dir/a.png", "b.png"))
	require.Nil(t, f.Get("dir/a.png"))
	require.Equal(t, "x", f.Get("b.png").Tool)
	require.False(t, f.Move("missing", "c.png"))
	require.False(t, f.Move("..", "c.png"))
	require.False(t, f.Move("b.png", ".."))

	require.False(t, f.Delete(".."))
	require.False(t, f.Delete("nope"))
	require.True(t, f.Delete("b.png"))
	require.Empty(t, f.Files)
}

func TestHashing(t *testing.T) {
	h, err := HashContent(strings.NewReader("abc"))
	require.NoError(t, err)
	require.Equal(t, HashBytes([]byte("abc")), h)
	require.Regexp(t, contentHashPattern, h)
	_, err = HashContent(iotest.ErrReader(errors.New("boom")))
	require.ErrorContains(t, err, "boom")
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", PromptHash("abc"))
}

func TestStatus(t *testing.T) {
	h := HashBytes([]byte("a"))
	other := HashBytes([]byte("b"))
	require.Equal(t, StatusMissing, Status(nil, h))
	require.Equal(t, StatusRecorded, Status(&Record{}, h))
	require.Equal(t, StatusRecorded, Status(&Record{ContentHash: h}, h))
	require.Equal(t, StatusModified, Status(&Record{ContentHash: h}, other))
	require.Equal(t, StatusEdited, Status(&Record{ContentHash: h, HumanEdited: true}, other))
}
