package assetbudget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetdiff"
)

const sample = `version: 1
rules:
  - name: textures
    paths: ["Assets/Textures/**", "*.png"]
    max_texture_size: 2048
    power_of_two: true
    max_bytes: 8MiB
  - name: ui
    paths: ["Assets/Textures/UI/**"]
    power_of_two: false
    max_texture_size: 4096
  - name: characters
    paths: ["Assets/Characters/**/*.fbx", "**/*.glb"]
    max_triangles: 50000
    max_vertices: 80000
  - name: audio
    paths: ["Assets/Audio/**"]
    max_audio_seconds: 120
    max_peak_dbfs: -1.0
  - name: naming
    paths: ["Assets/**"]
    name_pattern: "^[A-Za-z0-9_\\-.]+$"
  - paths: ["Build/"]
    max_bytes: 1024
`

func mustParse(t *testing.T, s string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(s))
	require.NoError(t, err)
	return cfg
}

func ptr[T any](v T) *T { return &v }

func TestParseSize(t *testing.T) {
	for in, want := range map[string]Size{
		"12": 12, "8MiB": 8 << 20, "8 mib": 8 << 20, "1.5GB": 1_500_000_000, "2KB": 2000,
		"3KiB": 3072, "1k": 1024, "2M": 2 << 20, "1G": 1 << 30, "5b": 5, "1gib": 1 << 30, "4mb": 4_000_000,
	} {
		got, err := ParseSize(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "MiB", "8 XB", "-1", "1e3", "99999999999999999999GiB"} {
		_, err := ParseSize(in)
		assert.Error(t, err, in)
	}
}

func TestParse(t *testing.T) {
	cfg := mustParse(t, sample)
	require.Equal(t, 1, cfg.Version)
	require.Len(t, cfg.Rules, 6)
	assert.Equal(t, Size(8<<20), *cfg.Rules[0].MaxBytes)
	assert.Equal(t, "rules[5]", cfg.Rules[5].Name)
	assert.Equal(t, Size(1024), *cfg.Rules[5].MaxBytes)

	empty := mustParse(t, "")
	assert.Equal(t, 1, empty.Version)
	assert.Empty(t, empty.Rules)
	assert.Equal(t, 1, mustParse(t, "rules: []\n").Version)

	_, err := json.Marshal(cfg)
	require.NoError(t, err)
}

func TestParseErrors(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		"unknown key":      {"rules:\n  - paths: [a]\n    max_trianglez: 3\n", "line 3: field max_trianglez not found"},
		"unknown top":      {"verzion: 1\n", "line 1: field verzion not found"},
		"version":          {"version: 2\n", "unsupported version 2"},
		"no paths":         {"rules:\n  - name: x\n    max_bytes: 1\n", `line 2: rule "x": paths must list`},
		"only comments":    {"rules:\n  - paths: ['# c', '']\n", `line 2: rule "rules[0]": paths must list`},
		"bad regex":        {"rules:\n  - paths: [a]\n    name_pattern: '('\n", "name_pattern: error parsing regexp"},
		"zero triangles":   {"rules:\n  - paths: [a]\n    max_triangles: 0\n", "max_triangles must be positive"},
		"negative texture": {"rules:\n  - paths: [a]\n    max_texture_size: -5\n", "max_texture_size must be positive"},
		"zero vertices":    {"rules:\n  - paths: [a]\n    max_vertices: 0\n", "max_vertices must be positive"},
		"zero bytes":       {"rules:\n  - paths: [a]\n    max_bytes: 0\n", "max_bytes must be positive"},
		"bad size":         {"rules:\n  - paths: [a]\n    max_bytes: lots\n", `line 3: invalid size "lots"`},
		"bad unit":         {"rules:\n  - paths: [a]\n    max_bytes: 3 TB\n", `line 3: invalid size unit "TB"`},
		"size not scalar":  {"rules:\n  - paths: [a]\n    max_bytes: [1]\n", "line 3: max_bytes must be a number"},
		"zero seconds":     {"rules:\n  - paths: [a]\n    max_audio_seconds: 0\n", "max_audio_seconds must be positive"},
		"inf peak":         {"rules:\n  - paths: [a]\n    max_peak_dbfs: .inf\n", "max_peak_dbfs must be a finite number"},
		"wrong type":       {"rules:\n  - paths: [a]\n    max_triangles: many\n", "line 3:"},
		"syntax":           {"rules: [\n", "yaml:"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestRuleCompileWithoutLines(t *testing.T) {
	assert.Nil(t, ruleLines([]byte("[")))
	assert.Nil(t, ruleLines([]byte("version: 1\n")))
	r := &Rule{}
	require.Error(t, r.compile())
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root)
	require.NoError(t, err)
	require.Nil(t, cfg)

	require.NoError(t, os.WriteFile(filepath.Join(root, FileName), []byte(sample), 0o644))
	cfg, err = Load(root)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 6)

	require.NoError(t, os.WriteFile(filepath.Join(root, FileName), []byte("bogus: 1\n"), 0o644))
	_, err = Load(root)
	require.ErrorContains(t, err, FileName+": line 1")

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, FileName), 0o755))
	_, err = Load(dir)
	require.ErrorContains(t, err, "read "+FileName)
}

func TestForAndNeedsDecode(t *testing.T) {
	cfg := mustParse(t, sample)
	l := cfg.For("Assets/Textures/UI/button.png")
	require.True(t, l.Matched())
	assert.Equal(t, 4096, l.textureSize.value)
	assert.Equal(t, "ui", l.textureSize.rule)
	assert.False(t, l.powerOfTwo.value)
	assert.Equal(t, "textures", l.bytes.rule)
	assert.Equal(t, "naming", l.name.rule)

	assert.True(t, cfg.NeedsDecode("Assets/Textures/a.png"))
	assert.True(t, cfg.NeedsDecode("Assets/Audio/a.wav"))
	assert.True(t, cfg.NeedsDecode("x/hero.glb"))
	assert.False(t, cfg.NeedsDecode("Assets/readme.txt"))
	assert.False(t, cfg.NeedsDecode("Assets/Audio/notes.txt"))
	assert.False(t, cfg.NeedsDecode("Assets/Textures/a.wav"))
	assert.False(t, cfg.NeedsDecode("Build/out.bin"))
	assert.True(t, cfg.Covers("Build/out.bin"))
	assert.False(t, cfg.Covers("docs/a.md"))

	var nilCfg *Config
	assert.False(t, nilCfg.Covers("a"))
	assert.Empty(t, Check(nilCfg, []*assetdiff.Result{{Path: "a"}}))
}

func TestCheck(t *testing.T) {
	cfg := mustParse(t, sample)
	results := []*assetdiff.Result{
		nil,
		{Path: "Assets/Textures/big.png", Status: assetdiff.StatusModified, NewSize: 9 << 20,
			Image: &assetdiff.ImageResult{New: &assetdiff.ImageSide{Width: 4096, Height: 1000}}},
		{Path: "Assets/Textures/UI/wide.png", Status: assetdiff.StatusAdded, NewSize: 10,
			Image: &assetdiff.ImageResult{New: &assetdiff.ImageSide{Width: 3000, Height: 1000}}},
		{Path: "Assets/Textures/ok.png", Status: assetdiff.StatusAdded, NewSize: 10,
			Image: &assetdiff.ImageResult{Old: &assetdiff.ImageSide{Width: 9999, Height: 3}, New: &assetdiff.ImageSide{Width: 512, Height: 256}}},
		{Path: "Assets/Textures/gone.png", Status: assetdiff.StatusRemoved, NewSize: 99 << 20},
		{Path: "Assets/Characters/hero/hero.fbx", Status: assetdiff.StatusModified,
			Model: &assetdiff.ModelResult{New: &assetdiff.ModelSide{Triangles: 60000, Vertices: 90000}}},
		{Path: "props/rock.glb", Status: assetdiff.StatusAdded,
			Model: &assetdiff.ModelResult{New: &assetdiff.ModelSide{Triangles: 10, Vertices: 30}}},
		{Path: "Assets/Audio/theme.wav", Status: assetdiff.StatusAdded,
			Audio: &assetdiff.AudioResult{New: &assetdiff.AudioSide{Duration: 130.5, PeakDB: ptr(-0.2)}}},
		{Path: "Assets/Audio/quiet.wav", Status: assetdiff.StatusAdded,
			Audio: &assetdiff.AudioResult{New: &assetdiff.AudioSide{Duration: 3}}},
		{Path: "Assets/bad name.txt", Status: assetdiff.StatusAdded},
		{Path: "Build/huge.bin", Status: assetdiff.StatusAdded, NewSize: 3 << 30, Error: "the file is too large to compare"},
		{Path: "Build/broken.png", Status: assetdiff.StatusAdded, NewSize: 2048, Error: "png: invalid format"},
	}
	got := Check(cfg, results)
	type key struct{ path, limit, rule string }
	var keys []key
	for _, v := range got {
		keys = append(keys, key{v.Path, v.Limit, v.Rule})
		assert.NotEmpty(t, v.Message)
	}
	assert.Equal(t, []key{
		{"Assets/Audio/theme.wav", LimitAudioSeconds, "audio"},
		{"Assets/Audio/theme.wav", LimitPeakDBFS, "audio"},
		{"Assets/Characters/hero/hero.fbx", LimitTriangles, "characters"},
		{"Assets/Characters/hero/hero.fbx", LimitVertices, "characters"},
		{"Assets/Textures/big.png", LimitBytes, "textures"},
		{"Assets/Textures/big.png", LimitTextureSize, "textures"},
		{"Assets/Textures/big.png", LimitPowerOfTwo, "textures"},
		{"Assets/bad name.txt", LimitNamePattern, "naming"},
		{"Build/broken.png", LimitBytes, "rules[5]"},
		{"Build/huge.bin", LimitBytes, "rules[5]"},
	}, keys)

	byKey := map[string]Violation{}
	for _, v := range got {
		byKey[v.Path+"|"+v.Limit] = v
	}
	assert.Equal(t, "4096x1000 exceeds 2048px", byKey["Assets/Textures/big.png|max_texture_size"].Message)
	assert.Equal(t, "4096x1000", byKey["Assets/Textures/big.png|power_of_two"].Actual)
	assert.Equal(t, "9.0 MiB exceeds 8.0 MiB", byKey["Assets/Textures/big.png|max_bytes"].Message)
	assert.Equal(t, `name "bad name.txt" does not match ^[A-Za-z0-9_\-.]+$`, byKey["Assets/bad name.txt|name_pattern"].Message)
	assert.Equal(t, "130.50s exceeds 120s", byKey["Assets/Audio/theme.wav|max_audio_seconds"].Message)
	assert.Equal(t, "peak -0.20 dBFS exceeds -1 dBFS", byKey["Assets/Audio/theme.wav|max_peak_dbfs"].Message)
	assert.Equal(t, "2.0 KiB exceeds 1.0 KiB", byKey["Build/broken.png|max_bytes"].Message)
	assert.Equal(t, "60000 triangles exceed 50000", byKey["Assets/Characters/hero/hero.fbx|max_triangles"].Message)
	assert.Equal(t, "90000 vertices exceed 80000", byKey["Assets/Characters/hero/hero.fbx|max_vertices"].Message)

	assert.Empty(t, Check(mustParse(t, ""), results))
}

func TestHelpers(t *testing.T) {
	assert.True(t, powerOfTwo(1))
	assert.True(t, powerOfTwo(2048))
	assert.False(t, powerOfTwo(0))
	assert.False(t, powerOfTwo(1000))
	assert.Equal(t, "1.0 KiB", humanSize(1024))
	assert.Equal(t, "12 B", humanSize(12))
	assert.Equal(t, "3.0 GiB", humanSize(3<<30))
	assert.Equal(t, "2.0 TiB", humanSize(2<<40))
}

func TestCheckWith_RequireProvenance(t *testing.T) {
	cfg := mustParse(t, `version: 1
rules:
  - name: generated
    paths: ["Art/**"]
    require_provenance: true
  - name: sketches
    paths: ["Art/Sketches/**"]
    require_provenance: false
  - name: size
    paths: ["Art/**"]
    max_bytes: 1KiB
`)
	assert.True(t, cfg.RequiresProvenance("Art/hero.png"))
	assert.False(t, cfg.RequiresProvenance("Art/Sketches/a.png"))
	assert.False(t, cfg.RequiresProvenance("Other/a.png"))
	assert.False(t, cfg.NeedsDecode("Art/hero.png"))

	results := []*assetdiff.Result{
		{Path: "Art/hero.png", Status: assetdiff.StatusAdded, NewSize: 10},
		{Path: "Art/known.png", Status: assetdiff.StatusAdded, NewSize: 10},
		{Path: "Art/Sketches/a.png", Status: assetdiff.StatusAdded, NewSize: 10},
		{Path: "Art/gone.png", Status: assetdiff.StatusRemoved},
	}
	got := CheckWith(cfg, results, CheckOptions{HasRecord: func(p string) bool { return p == "Art/known.png" }})
	require.Len(t, got, 1)
	assert.Equal(t, Violation{Path: "Art/hero.png", Rule: "generated", Limit: LimitProvenance, Actual: false, Max: true, Message: "no provenance record"}, got[0])

	all := Check(cfg, results)
	require.Len(t, all, 2)
	assert.Equal(t, "Art/known.png", all[1].Path)

	var r Rule
	require.NoError(t, json.Unmarshal([]byte(`{"require_provenance":true}`), &r))
	assert.True(t, *r.RequireProvenance)
}
