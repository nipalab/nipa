package chunker

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigForFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		isBinary bool
		want     Config
	}{
		{"text keeps default", "main.go", false, DefaultConfig},
		{"text with asset extension keeps default", "notes.png", false, DefaultConfig},
		{"blender keeps small chunks", "scene.blend", true, DefaultConfig},
		{"photoshop keeps small chunks", "art.psd", true, DefaultConfig},
		{"maya binary keeps small chunks", "rig.mb", true, DefaultConfig},
		{"unknown binary is default binary", "data.bin", true, DefaultBinaryConfig},
		{"uppercase texture is packed", "tex.PNG", true, PackedAssetConfig},
		{"audio is packed", "boom.wav", true, PackedAssetConfig},
		{"nested asset is packed", "assets/audio/boom.ogg", true, PackedAssetConfig},
		{"no extension binary is default binary", "blob", true, DefaultBinaryConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ConfigForFile(tt.path, tt.isBinary))
		})
	}
}

func TestProfileConfigsAreValid(t *testing.T) {
	for name, cfg := range map[string]Config{
		"default":        DefaultConfig,
		"default binary": DefaultBinaryConfig,
		"packed asset":   PackedAssetConfig,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cfg.sanitized()
			require.NoError(t, err)
		})
	}
}

func TestMaxChunkSize(t *testing.T) {
	require.Equal(t, PackedAssetConfig.Max, MaxChunkSize())
	require.GreaterOrEqual(t, MaxChunkSize(), DefaultConfig.Max)
	require.GreaterOrEqual(t, MaxChunkSize(), DefaultBinaryConfig.Max)
}

func TestProbeBinary(t *testing.T) {
	t.Run("binary content", func(t *testing.T) {
		raw := []byte{0x00, 0x01, 0x02}
		r := bytes.NewReader(raw)
		got, err := ProbeBinary(r)
		require.NoError(t, err)
		require.True(t, got)

		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, raw, data, "probe must restore the read position")
	})

	t.Run("text content", func(t *testing.T) {
		r := bytes.NewReader([]byte("hello world"))
		got, err := ProbeBinary(r)
		require.NoError(t, err)
		require.False(t, got)
	})

	t.Run("empty content", func(t *testing.T) {
		r := bytes.NewReader(nil)
		got, err := ProbeBinary(r)
		require.NoError(t, err)
		require.False(t, got)
	})
}
