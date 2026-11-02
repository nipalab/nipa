package preview

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		path     string
		isBinary bool
		want     Kind
	}{
		{"art/hero.PNG", true, KindImage},
		{"tex/rock.dds", true, KindImage},
		{"tex/sky.exr", true, KindImage},
		{"src/hero.psd", true, KindImage},
		{"models/hero.fbx", true, KindModel},
		{"models/hero.gltf", false, KindModel},
		{"sfx/jump.wav", true, KindAudio},
		{"video/intro.mp4", true, KindVideo},
		{"fonts/ui.ttf", true, KindFont},
		{"loc/en.csv", false, KindTable},
		{"loc/broken.csv", true, KindOpaque},
		{"src/main.go", false, KindText},
		{"Assets/Hero.prefab", false, KindText},
		{"src/hero.blend", true, KindOpaque},
		{"Content/Hero.uasset", true, KindOpaque},
		{"README", false, KindText},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Classify(c.path, c.isBinary), c.path)
	}
}
