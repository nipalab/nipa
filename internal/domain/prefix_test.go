package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizePathPrefix(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty", in: "", want: ""},
		{name: "root slash", in: "/", want: ""},
		{name: "dot", in: ".", want: ""},
		{name: "simple", in: "assets", want: "assets"},
		{name: "nested", in: "assets/textures", want: "assets/textures"},
		{name: "leading slash", in: "/assets/textures", want: "assets/textures"},
		{name: "trailing slash", in: "assets/textures/", want: "assets/textures"},
		{name: "duplicate slashes", in: "assets//textures", want: "assets/textures"},
		{name: "dot segments", in: "assets/./textures", want: "assets/textures"},
		{name: "backslashes", in: "assets\\textures", want: "assets/textures"},
		{name: "spaces trimmed", in: "  assets  ", want: "assets"},
		{name: "parent traversal", in: "assets/../secret", wantErr: true},
		{name: "leading parent traversal", in: "../secret", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePathPrefix(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestPrefixCovers(t *testing.T) {
	tests := []struct {
		prefix string
		path   string
		want   bool
	}{
		{prefix: "", path: "assets/logo.png", want: true},
		{prefix: "", path: "", want: true},
		{prefix: "assets", path: "assets", want: true},
		{prefix: "assets", path: "assets/logo.png", want: true},
		{prefix: "assets", path: "assets/textures/wood.png", want: true},
		{prefix: "assets", path: "assets2/logo.png", want: false},
		{prefix: "assets", path: "asset", want: false},
		{prefix: "assets", path: "src/main.go", want: false},
		{prefix: "assets", path: "", want: false},
		{prefix: "assets", path: "/assets/logo.png", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.prefix+"|"+tt.path, func(t *testing.T) {
			require.Equal(t, tt.want, PrefixCovers(tt.prefix, tt.path))
		})
	}
}

func TestPrefixCanDescend(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		dir    string
		want   bool
	}{
		{name: "empty prefix anywhere", prefix: "", dir: "assets", want: true},
		{name: "empty dir", prefix: "assets", dir: "", want: true},
		{name: "prefix above dir", prefix: "assets", dir: "assets/textures", want: true},
		{name: "prefix equals dir", prefix: "assets/textures", dir: "assets/textures", want: true},
		{name: "prefix below dir", prefix: "assets/textures/wood", dir: "assets/textures", want: true},
		{name: "unrelated dir", prefix: "assets", dir: "src", want: false},
		{name: "sibling dir", prefix: "assets/textures", dir: "assets/audio", want: false},
		{name: "shared name prefix", prefix: "assets", dir: "assets2", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, PrefixCanDescend(tt.prefix, tt.dir))
		})
	}
}
