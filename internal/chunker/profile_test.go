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
		{"godot binary scene keeps small chunks", "levels/main.scn", true, DefaultConfig},
		{"godot binary resource keeps small chunks", "data/items.RES", true, DefaultConfig},
		{"unknown binary is default binary", "data.bin", true, DefaultBinaryConfig},
		{"uppercase texture is packed", "tex.PNG", true, PackedAssetConfig},
		{"audio is packed", "boom.wav", true, PackedAssetConfig},
		{"nested asset is packed", "assets/audio/boom.ogg", true, PackedAssetConfig},
		{"no extension binary is default binary", "blob", true, DefaultBinaryConfig},
		{"unreal asset keeps small chunks", "Content/Hero.uasset", true, DefaultConfig},
		{"unreal map keeps small chunks", "Maps/Level.UMAP", true, DefaultConfig},
		{"houdini scene keeps small chunks", "fx/smoke.hiplc", true, DefaultConfig},
		{"substance painter keeps small chunks", "tex/hero.spp", true, DefaultConfig},
		{"aif audio is packed", "sfx/hit.aif", true, PackedAssetConfig},
		{"opus audio is packed", "voice/line.opus", true, PackedAssetConfig},
		{"fmod bank is packed", "audio/Master.bank", true, PackedAssetConfig},
		{"bink video is packed", "movies/intro.bk2", true, PackedAssetConfig},
		{"avif image is packed", "ui/logo.avif", true, PackedAssetConfig},
		{"alembic cache is packed", "cache/cloth.abc", true, PackedAssetConfig},
		{"godot pack is packed", "build/game.pck", true, PackedAssetConfig},
		{"tarball is packed", "deps/sdk.tar", true, PackedAssetConfig},
		{"android bundle is packed", "build/app.aab", true, PackedAssetConfig},
		{"font is packed", "fonts/ui.woff2", true, PackedAssetConfig},
		{"gltf text keeps default", "models/hero.gltf", false, DefaultConfig},
		{"executable is default binary", "bin/tool.exe", true, DefaultBinaryConfig},
		{"roblox place keeps small chunks", "game.rbxl", true, DefaultConfig},
		{"rpg maker data keeps small chunks", "Data/Map001.rvdata2", true, DefaultConfig},
		{"aseprite keeps small chunks", "art/hero.aseprite", true, DefaultConfig},
		{"source texture is packed", "materials/wall.vtf", true, PackedAssetConfig},
		{"godot imported texture is packed", ".godot/imported/a.ctex", true, PackedAssetConfig},
		{"spine binary skeleton is packed", "anim/hero.skel", true, PackedAssetConfig},
		{"truetype font is packed", "fonts/ui.ttf", true, PackedAssetConfig},
		{"quake package is packed", "baseq3/pak0.pk3", true, PackedAssetConfig},
		{"binary unity terrain keeps small chunks", "Assets/Terrain.asset", true, DefaultConfig},
		{"binary unity scene keeps small chunks", "Assets/Main.unity", true, DefaultConfig},
		{"text unity scene keeps default", "Assets/Main.unity", false, DefaultConfig},
		{"binary ply is packed", "scans/statue.ply", true, PackedAssetConfig},
		{"binary stl is packed", "print/part.STL", true, PackedAssetConfig},
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

func TestProfileListsAreDisjoint(t *testing.T) {
	for ext := range editableBinaryExtensions {
		_, packed := packedAssetExtensions[ext]
		_, binaryOnly := binaryOnlyProfiles[ext]
		require.False(t, packed || binaryOnly, ext)
	}
	for ext := range packedAssetExtensions {
		_, binaryOnly := binaryOnlyProfiles[ext]
		require.False(t, binaryOnly, ext)
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

func TestIsLockablePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"assets/orc.png", true},
		{"art/hero.PSD", true},
		{"scene.blend", true},
		{"levels/main.scn", true},
		{"data/items.res", true},
		{"levels/main.tscn", false},
		{"data/items.tres", false},
		{"audio/loop.wav", true},
		{"models/rig.fbx", true},
		{"Content/Hero.uasset", true},
		{"sfx/hit.aif", true},
		{"audio/Master.bank", true},
		{"deps/sdk.tar.zst", true},
		{"models/hero.gltf", false},
		{"models/hero.obj", false},
		{"bin/tool.exe", false},
		{"game.rbxl", true},
		{"materials/wall.vtf", true},
		{"fonts/ui.ttf", true},
		{"Assets/Main.unity", false},
		{"Assets/Terrain.asset", false},
		{"Assets/Hero.prefab", false},
		{"scans/statue.ply", false},
		{"game.rbxlx", false},
		{"src/main.go", false},
		{"data/values.json", false},
		{"README.md", false},
		{"noext", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			require.Equal(t, tt.want, IsLockablePath(tt.path))
		})
	}
}
